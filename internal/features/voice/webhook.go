package voice

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/webhook"

	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
	"github.com/ananddub/ndiscord_backend/internal/shared/realtime"
)

// roomMeta is the JSON stored in LiveKit room metadata by JoinChannel.
type roomMeta struct {
	GuildID   string `json:"guildId"`
	ChannelID string `json:"channelId"`
}

func parseRoomMeta(r *livekit.Room) roomMeta {
	var m roomMeta
	if r != nil {
		_ = json.Unmarshal([]byte(r.Metadata), &m)
	}
	if m.ChannelID == "" && r != nil {
		m.ChannelID = r.Name
	}
	return m
}

// WebhookHandler receives all LiveKit room/participant events via HTTP
// POST and fans them out over NATS so every connected client gets
// real-time voice state updates. One instance, always running — the single
// source of truth for who's in which room, muted/deafened/video/screen.
type WebhookHandler struct {
	provider  *auth.SimpleKeyProvider
	nats      *realtime.Hub
	voiceSvc  *Service
	roomMetas sync.Map // room name → roomMeta cache
}

// NewWebhookHandler creates the handler. Register it on your HTTP mux as
// POST /livekit/webhook.
func NewWebhookHandler(cfg config.LiveKitConfig, nats *realtime.Hub, voiceSvc *Service) *WebhookHandler {
	return &WebhookHandler{
		provider: auth.NewSimpleKeyProvider(cfg.APIKey, cfg.APISecret),
		nats:     nats,
		voiceSvc: voiceSvc,
	}
}

func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	event, err := webhook.ReceiveWebhookEvent(r, h.provider)
	if err != nil {
		logger.Log.Warn().Err(err).Msg("livekit webhook auth failed")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	h.handleEvent(event)
	w.WriteHeader(http.StatusOK)
}

// resolveRoomMeta parses room metadata from the event and caches it. Track
// events don't carry Room.Metadata, so we fall back to the cached value
// from an earlier room_started/participant_joined event.
func (h *WebhookHandler) resolveRoomMeta(event *livekit.WebhookEvent) roomMeta {
	meta := parseRoomMeta(event.Room)
	if meta.GuildID != "" {
		h.roomMetas.Store(meta.ChannelID, meta)
		return meta
	}
	if cached, ok := h.roomMetas.Load(meta.ChannelID); ok {
		return cached.(roomMeta)
	}
	return meta
}

func (h *WebhookHandler) handleEvent(event *livekit.WebhookEvent) {
	if event == nil {
		return
	}

	meta := h.resolveRoomMeta(event)

	log := logger.Log.With().
		Str("event", event.GetEvent()).
		Str("channel", meta.ChannelID).
		Str("guild", meta.GuildID).
		Logger()

	switch event.GetEvent() {
	case "room_started":
		log.Info().Msg("voice room started")
		h.publish(meta, "room_started", nil)

	case "room_finished":
		log.Info().Msg("voice room finished")
		if h.voiceSvc != nil {
			_ = h.voiceSvc.ClearChannel(context.Background(), meta.ChannelID)
		}
		h.publish(meta, "room_finished", nil)
		h.roomMetas.Delete(meta.ChannelID)

	case "participant_joined":
		p := event.GetParticipant()
		if p == nil {
			return
		}
		log.Info().Str("user", p.Identity).Msg("participant joined")
		h.publish(meta, "join", map[string]any{
			"user_id":  p.Identity,
			"name":     p.Name,
			"sid":      p.Sid,
			"metadata": p.Metadata,
		})

	case "participant_left":
		p := event.GetParticipant()
		if p == nil {
			return
		}
		log.Info().Str("user", p.Identity).Msg("participant left")
		if h.voiceSvc != nil {
			_ = h.voiceSvc.RemoveParticipant(context.Background(), meta.ChannelID, p.Identity)
			h.voiceSvc.broadcastChannelState(context.Background(), meta.ChannelID, meta.GuildID)
		}
		h.publish(meta, "leave", map[string]any{
			"user_id": p.Identity,
		})

	case "track_published":
		p := event.GetParticipant()
		t := event.GetTrack()
		if p == nil || t == nil {
			return
		}
		log.Info().Str("user", p.Identity).Str("source", t.Source.String()).Msg("track published")
		h.publishTrackEvent(meta, p, t, true)

	case "track_unpublished":
		p := event.GetParticipant()
		t := event.GetTrack()
		if p == nil || t == nil {
			return
		}
		log.Info().Str("user", p.Identity).Str("source", t.Source.String()).Msg("track unpublished")
		h.publishTrackEvent(meta, p, t, false)

	case "participant_active":
		p := event.GetParticipant()
		if p == nil {
			return
		}
		h.publishParticipantState(meta, p)

	default:
		log.Debug().Msg("unhandled livekit event")
	}
}

// publish sends a VOICE_STATE_UPDATE event on the guild events subject so
// StreamGuildEvents subscribers get voice state alongside channel/member
// events in one unified stream. For DM calls (no guildID), falls back to
// the per-user DmCall subject.
func (h *WebhookHandler) publish(meta roomMeta, action string, extra map[string]any) {
	payload := map[string]any{
		"event":      "VOICE_STATE_UPDATE",
		"action":     action,
		"channel_id": meta.ChannelID,
		"guild_id":   meta.GuildID,
	}
	for k, v := range extra {
		payload[k] = v
	}
	if meta.GuildID != "" {
		_ = h.nats.Publish(realtime.GuildChannelVoice(meta.GuildID, meta.ChannelID), payload)
	} else if uid, ok := extra["user_id"].(string); ok {
		_ = h.nats.Publish(realtime.DmCall(uid), payload)
	}
}

// publishTrackEvent fires on track_published / track_unpublished.
// Reads from Redis to include the full state so webhook events don't
// overwrite gRPC-set fields (e.g. screen_share) with defaults.
func (h *WebhookHandler) publishTrackEvent(meta roomMeta, p *livekit.ParticipantInfo, t *livekit.TrackInfo, published bool) {
	trackType := "unknown"
	switch t.Source {
	case livekit.TrackSource_CAMERA:
		trackType = "video"
	case livekit.TrackSource_SCREEN_SHARE, livekit.TrackSource_SCREEN_SHARE_AUDIO:
		trackType = "screen_share"
	case livekit.TrackSource_MICROPHONE:
		trackType = "audio"
	}

	extra := map[string]any{
		"user_id":    p.Identity,
		"track_type": trackType,
		"published":  published,
		"metadata":   p.Metadata,
	}

	// Merge Redis state so we don't clobber fields set via gRPC toggles.
	if h.voiceSvc != nil {
		if ps, err := h.voiceSvc.GetParticipant(context.Background(), meta.ChannelID, p.Identity); err == nil && ps != nil {
			extra["self_mute"] = ps.SelfMute
			extra["self_deaf"] = ps.SelfDeaf
			extra["video"] = ps.Video
			extra["streaming"] = ps.ScreenShare
			extra["name"] = ps.DisplayName
		} else {
			extra["self_mute"] = !hasActiveAudio(p)
			extra["video"] = hasActiveVideo(p)
		}
	}

	h.publish(meta, "track_update", extra)
}

// publishParticipantState sends the full mute/video/screen state from Redis.
func (h *WebhookHandler) publishParticipantState(meta roomMeta, p *livekit.ParticipantInfo) {
	extra := map[string]any{
		"user_id":  p.Identity,
		"name":     p.Name,
		"metadata": p.Metadata,
	}
	if h.voiceSvc != nil {
		if ps, err := h.voiceSvc.GetParticipant(context.Background(), meta.ChannelID, p.Identity); err == nil && ps != nil {
			extra["self_mute"] = ps.SelfMute
			extra["self_deaf"] = ps.SelfDeaf
			extra["video"] = ps.Video
			extra["streaming"] = ps.ScreenShare
			extra["name"] = ps.DisplayName
		}
	}
	h.publish(meta, "state_sync", extra)
}

func hasActiveAudio(p *livekit.ParticipantInfo) bool {
	for _, t := range p.Tracks {
		if t.Source == livekit.TrackSource_MICROPHONE && !t.Muted {
			return true
		}
	}
	return false
}

func hasActiveVideo(p *livekit.ParticipantInfo) bool {
	for _, t := range p.Tracks {
		if t.Source == livekit.TrackSource_CAMERA && !t.Muted {
			return true
		}
	}
	return false
}
