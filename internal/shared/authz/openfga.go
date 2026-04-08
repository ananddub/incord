package authz

import (
	"context"
	"fmt"

	openfga "github.com/openfga/go-sdk/client"

	"github.com/ananddub/ndiscord_backend/internal/shared/config"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
)

// Client wraps OpenFGA SDK for authorization checks.
type Client struct {
	fga *openfga.OpenFgaClient
}

func NewClient(cfg config.OpenFGAConfig) (*Client, error) {
	storeID := cfg.StoreID

	// Auto-detect store if not configured
	if storeID == "" {
		tmpClient, err := openfga.NewSdkClient(&openfga.ClientConfiguration{ApiUrl: cfg.APIUrl})
		if err != nil {
			return nil, fmt.Errorf("failed to create openfga client: %w", err)
		}
		stores, err := tmpClient.ListStores(context.Background()).Execute()
		if err != nil {
			return nil, fmt.Errorf("failed to list openfga stores: %w", err)
		}
		for _, s := range stores.GetStores() {
			if s.GetName() == "ndiscord" {
				storeID = s.GetId()
				break
			}
		}
		if storeID == "" {
			return nil, fmt.Errorf("openfga store 'ndiscord' not found - run init first")
		}
	}

	fgaClient, err := openfga.NewSdkClient(&openfga.ClientConfiguration{
		ApiUrl:  cfg.APIUrl,
		StoreId: storeID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create openfga client: %w", err)
	}

	logger.Log.Info().Str("api_url", cfg.APIUrl).Str("store_id", storeID).Msg("connected to openfga")
	return &Client{fga: fgaClient}, nil
}

// WriteTuple creates a relationship tuple. e.g. user:alice is member of guild:123
func (c *Client) WriteTuple(ctx context.Context, user, relation, object string) error {
	if c == nil {
		return nil
	}
	body := openfga.ClientWriteRequest{
		Writes: []openfga.ClientTupleKey{
			{User: user, Relation: relation, Object: object},
		},
	}
	_, err := c.fga.Write(ctx).Body(body).Execute()
	if err != nil {
		logger.Log.Debug().Err(err).Str("user", user).Str("relation", relation).Str("object", object).Msg("openfga write (may already exist)")
		return nil // ignore duplicate tuple errors
	}
	return nil
}

// DeleteTuple removes a relationship tuple.
func (c *Client) DeleteTuple(ctx context.Context, user, relation, object string) error {
	if c == nil {
		return nil
	}
	body := openfga.ClientWriteRequest{
		Deletes: []openfga.ClientTupleKeyWithoutCondition{
			{User: user, Relation: relation, Object: object},
		},
	}
	_, err := c.fga.Write(ctx).Body(body).Execute()
	if err != nil {
		logger.Log.Debug().Err(err).Str("user", user).Str("relation", relation).Str("object", object).Msg("openfga delete")
	}
	return nil
}

// Check returns true if the user has the given relation on the object.
func (c *Client) Check(ctx context.Context, user, relation, object string) bool {
	if c == nil {
		return true // no authz = allow all (for tests without OpenFGA)
	}
	body := openfga.ClientCheckRequest{
		User:     user,
		Relation: relation,
		Object:   object,
	}
	resp, err := c.fga.Check(ctx).Body(body).Execute()
	if err != nil {
		logger.Log.Error().Err(err).Str("user", user).Str("relation", relation).Str("object", object).Msg("openfga check failed")
		return false
	}
	return resp.GetAllowed()
}

// === Convenience methods for ndiscord ===

func UserKey(userID string) string    { return "user:" + userID }
func GuildKey(guildID string) string   { return "guild:" + guildID }
func ChannelKey(channelID string) string { return "channel:" + channelID }

// Guild membership
func (c *Client) AddGuildOwner(ctx context.Context, userID, guildID string) error {
	return c.WriteTuple(ctx, UserKey(userID), "owner", GuildKey(guildID))
}

func (c *Client) AddGuildMember(ctx context.Context, userID, guildID string) error {
	return c.WriteTuple(ctx, UserKey(userID), "member", GuildKey(guildID))
}

func (c *Client) RemoveGuildMember(ctx context.Context, userID, guildID string) error {
	return c.DeleteTuple(ctx, UserKey(userID), "member", GuildKey(guildID))
}

func (c *Client) AddGuildAdmin(ctx context.Context, userID, guildID string) error {
	return c.WriteTuple(ctx, UserKey(userID), "admin", GuildKey(guildID))
}

func (c *Client) RemoveGuildAdmin(ctx context.Context, userID, guildID string) error {
	return c.DeleteTuple(ctx, UserKey(userID), "admin", GuildKey(guildID))
}

// Channel → Guild relationship
func (c *Client) SetChannelGuild(ctx context.Context, channelID, guildID string) error {
	return c.WriteTuple(ctx, GuildKey(guildID), "guild", ChannelKey(channelID))
}

// Permission checks
func (c *Client) CanManageGuild(ctx context.Context, userID, guildID string) bool {
	return c.Check(ctx, UserKey(userID), "can_manage_guild", GuildKey(guildID))
}

func (c *Client) CanKick(ctx context.Context, userID, guildID string) bool {
	return c.Check(ctx, UserKey(userID), "can_kick", GuildKey(guildID))
}

func (c *Client) CanBan(ctx context.Context, userID, guildID string) bool {
	return c.Check(ctx, UserKey(userID), "can_ban", GuildKey(guildID))
}

func (c *Client) CanManageRoles(ctx context.Context, userID, guildID string) bool {
	return c.Check(ctx, UserKey(userID), "can_manage_roles", GuildKey(guildID))
}

func (c *Client) CanManageChannels(ctx context.Context, userID, guildID string) bool {
	return c.Check(ctx, UserKey(userID), "can_manage_channels", GuildKey(guildID))
}

func (c *Client) CanViewChannel(ctx context.Context, userID, channelID string) bool {
	return c.Check(ctx, UserKey(userID), "viewer", ChannelKey(channelID))
}

func (c *Client) CanSendInChannel(ctx context.Context, userID, channelID string) bool {
	return c.Check(ctx, UserKey(userID), "sender", ChannelKey(channelID))
}

func (c *Client) CanManageChannel(ctx context.Context, userID, channelID string) bool {
	return c.Check(ctx, UserKey(userID), "manager", ChannelKey(channelID))
}
