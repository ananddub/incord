package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authv1 "github.com/ananddub/ndiscord_backend/gen/auth/v1"
	channelv1 "github.com/ananddub/ndiscord_backend/gen/channel/v1"
	guildv1 "github.com/ananddub/ndiscord_backend/gen/guild/v1"
)

// TestInviteLinkFlow simulates the deep-link invite flow:
//  1. Owner creates guild + channels + invite code
//  2. Bob opens the invite link → PreviewInvite (no auth needed beyond a token)
//  3. Bob accepts → JoinGuild returns guild + channels + member count in one call
func TestInviteLinkFlow(t *testing.T) {
	conn := newConn(t)
	authClient := authv1.NewAuthServiceClient(conn)
	guildClient := guildv1.NewGuildServiceClient(conn)
	channelClient := channelv1.NewChannelServiceClient(conn)

	ts := time.Now().UnixNano()
	owner := registerAndVerify(t, authClient,
		fmt.Sprintf("inviteowner%d", ts),
		fmt.Sprintf("inviteowner%d@test.local", ts),
		"testpass123")
	bob := registerAndVerify(t, authClient,
		fmt.Sprintf("invitebob%d", ts),
		fmt.Sprintf("invitebob%d@test.local", ts),
		"testpass123")

	// Owner creates guild.
	createResp, err := guildClient.CreateGuild(owner.ctx(), &guildv1.CreateGuildRequest{
		Name:        "Invite Link Guild",
		Description: "deep-link test",
	})
	require.NoError(t, err)
	guildID := createResp.Guild.Id

	// Owner creates three channels so we can assert the join returns them.
	for _, name := range []string{"general", "random", "announcements"} {
		_, err := channelClient.CreateChannel(owner.ctx(), &channelv1.CreateChannelRequest{
			GuildId: guildID,
			Name:    name,
			Type:    channelv1.ChannelType_CHANNEL_TYPE_TEXT,
		})
		require.NoError(t, err)
	}
	// Pick the first channel id for the invite (server allows empty channel uuid too,
	// but CreateInvite requires a valid uuid per proto validator).
	listResp, err := channelClient.ListGuildChannels(owner.ctx(), &channelv1.ListGuildChannelsRequest{GuildId: guildID})
	require.NoError(t, err)
	require.Len(t, listResp.Channels, 3)
	firstChannelID := listResp.Channels[0].Id

	// Owner creates invite.
	invResp, err := guildClient.CreateInvite(owner.ctx(), &guildv1.CreateInviteRequest{
		GuildId:       guildID,
		ChannelId:     firstChannelID,
		MaxUses:       5,
		MaxAgeSeconds: 3600,
	})
	require.NoError(t, err)
	inviteCode := invResp.Invite.Code
	require.NotEmpty(t, inviteCode)
	// CreateInvite should return a full shareable URL like {base}/{code}.
	require.NotEmpty(t, invResp.Invite.Url, "invite response must include url")
	assert.Contains(t, invResp.Invite.Url, "/"+inviteCode, "url must end with /<code>")

	// ── Preview (as if Bob clicked the link) ───────────────────────────────
	preview, err := guildClient.PreviewInvite(bob.ctx(), &guildv1.PreviewInviteRequest{
		InviteCode: inviteCode,
	})
	require.NoError(t, err)
	require.NotNil(t, preview.Preview)
	p := preview.Preview
	assert.Equal(t, inviteCode, p.Code)
	assert.Equal(t, guildID, p.GuildId)
	assert.Equal(t, "Invite Link Guild", p.GuildName)
	assert.Equal(t, int32(1), p.MemberCount, "only owner is in the guild pre-join")
	assert.Equal(t, int32(3), p.ChannelCount)
	assert.Equal(t, owner.Username, p.InviterUsername)
	assert.False(t, p.AlreadyMember, "bob hasn't joined yet")
	assert.Equal(t, int32(5), p.MaxUses)
	assert.Equal(t, int32(0), p.Uses)

	// ── Accept (Bob joins) ─────────────────────────────────────────────────
	joinResp, err := guildClient.JoinGuild(bob.ctx(), &guildv1.JoinGuildRequest{
		InviteCode: inviteCode,
	})
	require.NoError(t, err)
	assert.Equal(t, guildID, joinResp.Guild.Id)
	assert.Equal(t, int32(2), joinResp.MemberCount, "bob is now a member")
	require.Len(t, joinResp.Channels, 3, "join should return all channels in one call")

	// Verify channel names come through.
	names := map[string]bool{}
	for _, c := range joinResp.Channels {
		names[c.Name] = true
	}
	assert.True(t, names["general"])
	assert.True(t, names["random"])
	assert.True(t, names["announcements"])

	// Preview after join should report already_member = true.
	preview2, err := guildClient.PreviewInvite(bob.ctx(), &guildv1.PreviewInviteRequest{
		InviteCode: inviteCode,
	})
	require.NoError(t, err)
	assert.True(t, preview2.Preview.AlreadyMember)
	assert.Equal(t, int32(2), preview2.Preview.MemberCount)
	assert.Equal(t, int32(1), preview2.Preview.Uses, "invite use count incremented")
}
