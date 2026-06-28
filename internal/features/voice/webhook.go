package voice

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/livekit/protocol/webhook"
	"google.golang.org/protobuf/types/known/timestamppb"

	streamv1 "github.com/ananddub/ndiscord_backend/gen/stream/v1"
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

type WebhookHandler struct {
	provider  *auth.SimpleKeyProvider
	lpb       *realtime.LPubSub
	voiceSvc  *Service
	roomMetas sync.Map // room name → roomMeta cache
}

func NewWebhookHandler(cfg config.LiveKitConfig, ldp *realtime.LPubSub, voiceSvc *Service) *WebhookHandler {
	return &WebhookHandler{
		provider: auth.NewSimpleKeyProvider(cfg.APIKey, cfg.APISecret),
		lpb:      ldp,
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
		h.publish(meta, &streamv1.VoiceStateEvent{Action: streamv1.VoiceAction_VOICE_ACTION_ROOM_STARTED})

	case "room_finished":
		log.Info().Msg("voice room finished")
		if h.voiceSvc != nil {
			_ = h.voiceSvc.ClearChannel(context.Background(), meta.ChannelID)
			_ = h.voiceSvc.ClearActiveSince(context.Background(), meta.ChannelID)
		}
		h.publish(meta, &streamv1.VoiceStateEvent{Action: streamv1.VoiceAction_VOICE_ACTION_ROOM_FINISHED})
		h.roomMetas.Delete(meta.ChannelID)

	case "participant_joined":
		p := event.GetParticipant()
		if p == nil {
			return
		}
		log.Info().Str("user", p.Identity).Msg("participant joined")
		h.publish(meta, &streamv1.VoiceStateEvent{
			Action:   streamv1.VoiceAction_VOICE_ACTION_JOIN,
			UserId:   p.Identity,
			Name:     p.Name,
			Sid:      p.Sid,
			Metadata: p.Metadata,
		})

	case "participant_left":
		p := event.GetParticipant()
		if p == nil {
			return
		}
		log.Info().Str("user", p.Identity).Msg("participant left")
		if h.voiceSvc != nil {
			_ = h.voiceSvc.RemoveParticipant(context.Background(), meta.ChannelID, p.Identity)
			h.voiceSvc.broadcastChannelState(context.Background(), time.Now(), meta.ChannelID, meta.GuildID)
		}
		h.publish(meta, &streamv1.VoiceStateEvent{
			Action: streamv1.VoiceAction_VOICE_ACTION_LEAVE,
			UserId: p.Identity,
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

// publish sends a typed VoiceStateEvent on the guild voice subject so
// StreamVoiceState subscribers get it alongside every other voice update.
// Fills in the boilerplate fields (event, channel_id, guild_id, timestamp,
// room_active_since) so callers only have to populate action + identity +
// state flags. For DM calls (no guildID), falls back to the per-user DmCall
// subject.
func (h *WebhookHandler) publish(meta roomMeta, evt *streamv1.VoiceStateEvent) {
	if evt == nil {
		return
	}
	evt.Event = streamv1.VoiceEvent_VOICE_EVENT_STATE_UPDATE
	evt.ChannelId = meta.ChannelID
	evt.GuildId = meta.GuildID
	if evt.Timestamp == nil {
		evt.Timestamp = timestamppb.Now()
	}
	if evt.RoomActiveSince == 0 && h.voiceSvc != nil && meta.ChannelID != "" {
		evt.RoomActiveSince = h.voiceSvc.GetActiveSince(context.Background(), meta.ChannelID)
	}
	if meta.GuildID != "" {
		_ = realtime.Publish(h.lpb, realtime.GuildChannelVoice(meta.GuildID, meta.ChannelID), evt)
	} else if evt.UserId != "" {
		_ = realtime.Publish(h.lpb, realtime.DmCall(evt.UserId), evt)
	}
}

// publishTrackEvent fires on track_published / track_unpublished.
// Reads from Redis to include the full state so webhook events don't
// overwrite gRPC-set fields (e.g. screen_share) with defaults.
func (h *WebhookHandler) publishTrackEvent(meta roomMeta, p *livekit.ParticipantInfo, t *livekit.TrackInfo, published bool) {
	trackType := streamv1.VoiceTrackType_VOICE_TRACK_UNSPECIFIED
	switch t.Source {
	case livekit.TrackSource_CAMERA:
		trackType = streamv1.VoiceTrackType_VOICE_TRACK_VIDEO
	case livekit.TrackSource_SCREEN_SHARE, livekit.TrackSource_SCREEN_SHARE_AUDIO:
		trackType = streamv1.VoiceTrackType_VOICE_TRACK_SCREEN_SHARE
	case livekit.TrackSource_MICROPHONE:
		trackType = streamv1.VoiceTrackType_VOICE_TRACK_AUDIO
	}

	evt := &streamv1.VoiceStateEvent{
		Action:    streamv1.VoiceAction_VOICE_ACTION_TRACK_UPDATE,
		UserId:    p.Identity,
		TrackType: trackType,
		Published: published,
		Metadata:  p.Metadata,
	}

	// Merge Redis state so we don't clobber fields set via gRPC toggles.
	if h.voiceSvc != nil {
		if ps, err := h.voiceSvc.GetParticipant(context.Background(), meta.ChannelID, p.Identity); err == nil && ps != nil {
			evt.SelfMute = ps.SelfMute
			evt.SelfDeaf = ps.SelfDeaf
			evt.Video = ps.Video
			evt.Streaming = ps.ScreenShare
			evt.Name = ps.DisplayName
		} else {
			evt.SelfMute = !hasActiveAudio(p)
			evt.Video = hasActiveVideo(p)
		}
	}

	h.publish(meta, evt)
}

// publishParticipantState sends the full mute/video/screen state from Redis.
func (h *WebhookHandler) publishParticipantState(meta roomMeta, p *livekit.ParticipantInfo) {
	evt := &streamv1.VoiceStateEvent{
		Action:   streamv1.VoiceAction_VOICE_ACTION_STATE_SYNC,
		UserId:   p.Identity,
		Name:     p.Name,
		Metadata: p.Metadata,
	}
	if h.voiceSvc != nil {
		if ps, err := h.voiceSvc.GetParticipant(context.Background(), meta.ChannelID, p.Identity); err == nil && ps != nil {
			evt.SelfMute = ps.SelfMute
			evt.SelfDeaf = ps.SelfDeaf
			evt.Video = ps.Video
			evt.Streaming = ps.ScreenShare
			evt.Name = ps.DisplayName
		}
	}
	h.publish(meta, evt)
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
