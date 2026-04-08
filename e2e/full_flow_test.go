// Package e2e runs end-to-end tests against a running ndiscord server.
// Requires: docker compose up (server on localhost:50051)
//
// Run: go test ./e2e/... -v -tags=e2e -timeout 60s
package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authv1 "github.com/ananddub/ndiscord_backend/gen/auth/v1"
	channelv1 "github.com/ananddub/ndiscord_backend/gen/channel/v1"
	guildv1 "github.com/ananddub/ndiscord_backend/gen/guild/v1"
	mediav1 "github.com/ananddub/ndiscord_backend/gen/media/v1"
	messagev1 "github.com/ananddub/ndiscord_backend/gen/message/v1"
	presencev1 "github.com/ananddub/ndiscord_backend/gen/presence/v1"
	userv1 "github.com/ananddub/ndiscord_backend/gen/user/v1"
	voicev1 "github.com/ananddub/ndiscord_backend/gen/voice/v1"
)

// clients holds all gRPC service clients
type clients struct {
	auth     authv1.AuthServiceClient
	user     userv1.UserServiceClient
	guild    guildv1.GuildServiceClient
	channel  channelv1.ChannelServiceClient
	message  messagev1.MessageServiceClient
	presence presencev1.PresenceServiceClient
	media    mediav1.MediaServiceClient
	voice    voicev1.VoiceServiceClient
}

func setupClients(t *testing.T) *clients {
	t.Helper()
	conn := newConn(t)

	return &clients{
		auth:     authv1.NewAuthServiceClient(conn),
		user:     userv1.NewUserServiceClient(conn),
		guild:    guildv1.NewGuildServiceClient(conn),
		channel:  channelv1.NewChannelServiceClient(conn),
		message:  messagev1.NewMessageServiceClient(conn),
		presence: presencev1.NewPresenceServiceClient(conn),
		media:    mediav1.NewMediaServiceClient(conn),
		voice:    voicev1.NewVoiceServiceClient(conn),
	}
}

// ============================================================
// TEST: Full Discord Flow - everything together
// ============================================================

func TestFullDiscordFlow(t *testing.T) {
	c := setupClients(t)
	ts := fmt.Sprintf("%d", time.Now().UnixNano()) // unique suffix to avoid conflicts

	// ──────────────────────────────────────
	// 1. AUTH: Register two users
	// ──────────────────────────────────────
	t.Run("01_Register_Users", func(t *testing.T) {
		alice := registerAndVerify(t, c.auth, "alice_"+ts, "alice_"+ts+"@test.com", "password123")
		assert.NotEmpty(t, alice.ID)
		assert.NotEmpty(t, alice.AccessToken)

		bob := registerAndVerify(t, c.auth, "bob_"+ts, "bob_"+ts+"@test.com", "password456")
		assert.NotEmpty(t, bob.ID)
		assert.NotEqual(t, alice.ID, bob.ID)
	})

	// Register users for remaining tests
	alice := registerAndVerify(t, c.auth, "a_"+ts, "a_"+ts+"@test.com", "password123")
	bob := registerAndVerify(t, c.auth, "b_"+ts, "b_"+ts+"@test.com", "password123")
	charlie := registerAndVerify(t, c.auth, "c_"+ts, "c_"+ts+"@test.com", "password123")

	// ──────────────────────────────────────
	// 2. AUTH: Login + Token flow
	// ──────────────────────────────────────
	t.Run("02_Login_And_Tokens", func(t *testing.T) {
		// Login
		loginResp, err := c.auth.Login(context.Background(), &authv1.LoginRequest{
			Email: "a_" + ts + "@test.com", Password: "password123",
		})
		require.NoError(t, err)
		assert.Equal(t, alice.ID, loginResp.UserId)

		// Validate token
		valResp, err := c.auth.ValidateToken(context.Background(), &authv1.ValidateTokenRequest{
			AccessToken: loginResp.AccessToken,
		})
		require.NoError(t, err)
		assert.True(t, valResp.Valid)

		// Refresh token
		refResp, err := c.auth.RefreshToken(context.Background(), &authv1.RefreshTokenRequest{
			RefreshToken: loginResp.RefreshToken,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, refResp.AccessToken)
		assert.NotEqual(t, loginResp.RefreshToken, refResp.RefreshToken, "refresh token should rotate")
	})

	// ──────────────────────────────────────
	// 3. USER: Profile operations
	// ──────────────────────────────────────
	t.Run("03_User_Profile", func(t *testing.T) {
		// Get own profile
		resp, err := c.user.GetUser(alice.ctx(), &userv1.GetUserRequest{UserId: alice.ID})
		require.NoError(t, err)
		assert.Equal(t, "a_"+ts, resp.User.Username)

		// Update bio
		updateResp, err := c.user.UpdateUser(alice.ctx(), &userv1.UpdateUserRequest{
			UserId: alice.ID, Bio: strPtr("Discord clone tester"),
		})
		require.NoError(t, err)
		assert.Equal(t, "Discord clone tester", updateResp.User.Bio)

		// Search users
		searchResp, err := c.user.SearchUsers(alice.ctx(), &userv1.SearchUsersRequest{
			Query: "b_" + ts, Limit: 10,
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(searchResp.Users), 1)
	})

	// ──────────────────────────────────────
	// 4. FRIENDS: Request → Accept → List
	// ──────────────────────────────────────
	t.Run("04_Friends", func(t *testing.T) {
		// Alice sends friend request to Bob
		_, err := c.user.SendFriendRequest(alice.ctx(), &userv1.SendFriendRequestRequest{
			TargetUserId: bob.ID,
		})
		require.NoError(t, err)

		// Bob sees pending request
		pending, err := c.user.ListPendingRequests(bob.ctx(), &userv1.ListPendingRequestsRequest{})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(pending.Incoming), 1)

		// Bob accepts
		_, err = c.user.AcceptFriendRequest(bob.ctx(), &userv1.AcceptFriendRequestRequest{
			RequesterUserId: alice.ID,
		})
		require.NoError(t, err)

		// Both see each other in friends list
		aliceFriends, err := c.user.ListFriends(alice.ctx(), &userv1.ListFriendsRequest{})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(aliceFriends.Friends), 1)

		bobFriends, err := c.user.ListFriends(bob.ctx(), &userv1.ListFriendsRequest{})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(bobFriends.Friends), 1)
	})

	// ──────────────────────────────────────
	// 5. GUILD: Create → Invite → Join
	// ──────────────────────────────────────
	var guildID string
	t.Run("05_Guild_Create_And_Join", func(t *testing.T) {
		// Alice creates a guild
		guildResp, err := c.guild.CreateGuild(alice.ctx(), &guildv1.CreateGuildRequest{
			Name: "Test Server " + ts, Description: "E2E test guild",
		})
		require.NoError(t, err)
		guildID = guildResp.Guild.Id
		assert.Equal(t, "Test Server "+ts, guildResp.Guild.Name)
		assert.Equal(t, alice.ID, guildResp.Guild.OwnerId)
		assert.Equal(t, int32(1), guildResp.Guild.MemberCount)

		// Verify guild exists
		getResp, err := c.guild.GetGuild(alice.ctx(), &guildv1.GetGuildRequest{GuildId: guildID})
		require.NoError(t, err)
		assert.Equal(t, guildID, getResp.Guild.Id)
	})

	// ──────────────────────────────────────
	// 6. CHANNEL: Create text + voice channels
	// ──────────────────────────────────────
	var textChannelID, voiceChannelID string
	t.Run("06_Channels", func(t *testing.T) {
		// Create text channel
		chResp, err := c.channel.CreateChannel(alice.ctx(), &channelv1.CreateChannelRequest{
			GuildId: guildID, Name: "general", Type: channelv1.ChannelType_CHANNEL_TYPE_TEXT,
			Topic: "Welcome to the server!",
		})
		require.NoError(t, err)
		textChannelID = chResp.Channel.Id
		assert.Equal(t, "general", chResp.Channel.Name)

		// Create voice channel
		vChResp, err := c.channel.CreateChannel(alice.ctx(), &channelv1.CreateChannelRequest{
			GuildId: guildID, Name: "Voice Chat", Type: channelv1.ChannelType_CHANNEL_TYPE_VOICE,
		})
		require.NoError(t, err)
		voiceChannelID = vChResp.Channel.Id

		// List guild channels
		listResp, err := c.channel.ListGuildChannels(alice.ctx(), &channelv1.ListGuildChannelsRequest{
			GuildId: guildID,
		})
		require.NoError(t, err)
		assert.Len(t, listResp.Channels, 2)
	})

	// ──────────────────────────────────────
	// 7. INVITE: Create invite → Bob joins
	// ──────────────────────────────────────
	t.Run("07_Invite_And_Join", func(t *testing.T) {
		// Alice creates invite
		invResp, err := c.guild.CreateInvite(alice.ctx(), &guildv1.CreateInviteRequest{
			GuildId: guildID, ChannelId: textChannelID, MaxUses: 10, MaxAgeSeconds: 3600,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, invResp.Invite.Code)

		// Bob joins via invite
		joinResp, err := c.guild.JoinGuild(bob.ctx(), &guildv1.JoinGuildRequest{
			InviteCode: invResp.Invite.Code,
		})
		require.NoError(t, err)
		assert.Equal(t, guildID, joinResp.Guild.Id)

		// Charlie also joins
		_, err = c.guild.JoinGuild(charlie.ctx(), &guildv1.JoinGuildRequest{
			InviteCode: invResp.Invite.Code,
		})
		require.NoError(t, err)

		// Verify 3 members
		members, err := c.guild.ListMembers(alice.ctx(), &guildv1.ListMembersRequest{
			GuildId: guildID, Limit: 10,
		})
		require.NoError(t, err)
		assert.Len(t, members.Members, 3)
	})

	// ──────────────────────────────────────
	// 8. ROLES: Create → Assign → Verify
	// ──────────────────────────────────────
	t.Run("08_Roles", func(t *testing.T) {
		// Create moderator role
		roleResp, err := c.guild.CreateRole(alice.ctx(), &guildv1.CreateRoleRequest{
			GuildId: guildID, Name: "Moderator", Color: "#FF5733",
		})
		require.NoError(t, err)
		assert.Equal(t, "Moderator", roleResp.Role.Name)

		// Assign role to Bob
		_, err = c.guild.AssignRole(alice.ctx(), &guildv1.AssignRoleRequest{
			GuildId: guildID, UserId: bob.ID, RoleId: roleResp.Role.Id,
		})
		require.NoError(t, err)

		// Remove role
		_, err = c.guild.RemoveRole(alice.ctx(), &guildv1.RemoveRoleRequest{
			GuildId: guildID, UserId: bob.ID, RoleId: roleResp.Role.Id,
		})
		require.NoError(t, err)
	})

	// ──────────────────────────────────────
	// 9. MESSAGES: Send → Edit → React → List
	// ──────────────────────────────────────
	var msgID string
	t.Run("09_Messages", func(t *testing.T) {
		// Alice sends a message
		sendResp, err := c.message.SendMessage(alice.ctx(), &messagev1.SendMessageRequest{
			ChannelId: textChannelID, Content: "Hello everyone! " + ts,
		})
		require.NoError(t, err)
		msgID = sendResp.Message.Id
		assert.Equal(t, "Hello everyone! "+ts, sendResp.Message.Content)
		assert.Equal(t, alice.ID, sendResp.Message.AuthorId)

		// Bob sends a reply
		replyResp, err := c.message.SendMessage(bob.ctx(), &messagev1.SendMessageRequest{
			ChannelId: textChannelID, Content: "Hey Alice!", ReplyToId: msgID,
		})
		require.NoError(t, err)
		assert.Equal(t, msgID, replyResp.Message.ReplyToId)

		// Alice edits her message
		editResp, err := c.message.EditMessage(alice.ctx(), &messagev1.EditMessageRequest{
			ChannelId: textChannelID, MessageId: msgID, Content: "Hello everyone! (edited) " + ts,
		})
		require.NoError(t, err)
		assert.Contains(t, editResp.Message.Content, "(edited)")

		// Bob reacts to Alice's message
		_, err = c.message.AddReaction(bob.ctx(), &messagev1.AddReactionRequest{
			ChannelId: textChannelID, MessageId: msgID, Emoji: "\U0001f44d",
		})
		require.NoError(t, err)

		// Charlie also reacts
		_, err = c.message.AddReaction(charlie.ctx(), &messagev1.AddReactionRequest{
			ChannelId: textChannelID, MessageId: msgID, Emoji: "\U0001f44d",
		})
		require.NoError(t, err)

		// List messages - should see both
		listResp, err := c.message.ListMessages(alice.ctx(), &messagev1.ListMessagesRequest{
			ChannelId: textChannelID, Limit: 10,
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(listResp.Messages), 2)

		// Pin the message
		_, err = c.message.PinMessage(alice.ctx(), &messagev1.PinMessageRequest{
			ChannelId: textChannelID, MessageId: msgID,
		})
		require.NoError(t, err)

		// Verify pin
		getResp, err := c.message.GetMessage(alice.ctx(), &messagev1.GetMessageRequest{
			ChannelId: textChannelID, MessageId: msgID,
		})
		require.NoError(t, err)
		assert.True(t, getResp.Message.Pinned)
	})

	// ──────────────────────────────────────
	// 10. TYPING: Start typing indicator
	// ──────────────────────────────────────
	t.Run("10_Typing", func(t *testing.T) {
		_, err := c.message.StartTyping(bob.ctx(), &messagev1.StartTypingRequest{
			ChannelId: textChannelID,
		})
		require.NoError(t, err)
	})

	// ──────────────────────────────────────
	// 11. ACK: Mark message as read
	// ──────────────────────────────────────
	t.Run("11_Ack_Message", func(t *testing.T) {
		_, err := c.message.AckMessage(bob.ctx(), &messagev1.AckMessageRequest{
			ChannelId: textChannelID, MessageId: msgID,
		})
		require.NoError(t, err)
	})

	// ──────────────────────────────────────
	// 12. DM: Create DM channel between users
	// ──────────────────────────────────────
	t.Run("12_Direct_Messages", func(t *testing.T) {
		// Alice creates DM with Bob
		dmResp, err := c.channel.CreateDMChannel(alice.ctx(), &channelv1.CreateDMChannelRequest{
			RecipientIds: []string{bob.ID},
		})
		require.NoError(t, err)
		assert.Equal(t, channelv1.ChannelType_CHANNEL_TYPE_DM, dmResp.Channel.Type)

		// Send DM
		dmMsgResp, err := c.message.SendMessage(alice.ctx(), &messagev1.SendMessageRequest{
			ChannelId: dmResp.Channel.Id, Content: "Hey Bob, private message!",
		})
		require.NoError(t, err)
		assert.Equal(t, "Hey Bob, private message!", dmMsgResp.Message.Content)

		// List DM channels
		listDMs, err := c.channel.ListDMChannels(alice.ctx(), &channelv1.ListDMChannelsRequest{})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(listDMs.Channels), 1)
	})

	// ──────────────────────────────────────
	// 13. PRESENCE: Update and check status
	// ──────────────────────────────────────
	t.Run("13_Presence", func(t *testing.T) {
		// Alice goes online
		_, err := c.presence.UpdatePresence(alice.ctx(), &presencev1.UpdatePresenceRequest{
			Status: presencev1.Status_STATUS_ONLINE, CustomStatus: "Playing games",
		})
		require.NoError(t, err)

		// Check Alice's presence
		presResp, err := c.presence.GetPresence(alice.ctx(), &presencev1.GetPresenceRequest{
			UserId: alice.ID,
		})
		require.NoError(t, err)
		assert.Equal(t, presencev1.Status_STATUS_ONLINE, presResp.Presence.Status)
		assert.Equal(t, "Playing games", presResp.Presence.CustomStatus)

		// Bob goes DND
		_, err = c.presence.UpdatePresence(bob.ctx(), &presencev1.UpdatePresenceRequest{
			Status: presencev1.Status_STATUS_DND,
		})
		require.NoError(t, err)

		// Bulk presence check
		bulkResp, err := c.presence.GetBulkPresence(alice.ctx(), &presencev1.GetBulkPresenceRequest{
			UserIds: []string{alice.ID, bob.ID, charlie.ID},
		})
		require.NoError(t, err)
		assert.Len(t, bulkResp.Presences, 3)
	})

	// ──────────────────────────────────────
	// 14. MEDIA: Request upload
	// ──────────────────────────────────────
	t.Run("14_Media_Upload", func(t *testing.T) {
		uploadResp, err := c.media.RequestUpload(alice.ctx(), &mediav1.RequestUploadRequest{
			Filename: "screenshot.png", ContentType: "image/png", Size: 1024000,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, uploadResp.UploadId)
		assert.NotEmpty(t, uploadResp.UploadUrl, "should have presigned URL")
	})

	// ──────────────────────────────────────
	// 15. VOICE: Join voice channel
	// ──────────────────────────────────────
	t.Run("15_Voice", func(t *testing.T) {
		// Alice joins voice
		joinResp, err := c.voice.JoinChannel(alice.ctx(), &voicev1.JoinChannelRequest{
			GuildId: guildID, ChannelId: voiceChannelID,
		})
		require.NoError(t, err)
		assert.NotEmpty(t, joinResp.SessionId)
		assert.NotEmpty(t, joinResp.UdpEndpoint)
		assert.NotZero(t, joinResp.Ssrc)
		assert.NotEmpty(t, joinResp.EncryptionKey)

		// Bob joins voice too
		bobJoin, err := c.voice.JoinChannel(bob.ctx(), &voicev1.JoinChannelRequest{
			GuildId: guildID, ChannelId: voiceChannelID,
		})
		require.NoError(t, err)
		assert.NotEqual(t, joinResp.Ssrc, bobJoin.Ssrc, "each user gets unique SSRC")

		// Check participants
		partResp, err := c.voice.GetChannelParticipants(alice.ctx(), &voicev1.GetChannelParticipantsRequest{
			ChannelId: voiceChannelID,
		})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(partResp.Participants), 2)

		// Alice mutes
		_, err = c.voice.UpdateVoiceState(alice.ctx(), &voicev1.UpdateVoiceStateRequest{
			ChannelId: voiceChannelID, SelfMute: true,
		})
		require.NoError(t, err)

		// Alice leaves
		_, err = c.voice.LeaveChannel(alice.ctx(), &voicev1.LeaveChannelRequest{
			GuildId: guildID, ChannelId: voiceChannelID,
		})
		require.NoError(t, err)

		// Bob leaves
		_, err = c.voice.LeaveChannel(bob.ctx(), &voicev1.LeaveChannelRequest{
			GuildId: guildID, ChannelId: voiceChannelID,
		})
		require.NoError(t, err)
	})

	// ──────────────────────────────────────
	// 16. BLOCK: Alice blocks Charlie
	// ──────────────────────────────────────
	t.Run("16_Block_User", func(t *testing.T) {
		_, err := c.user.BlockUser(alice.ctx(), &userv1.BlockUserRequest{
			TargetUserId: charlie.ID,
		})
		require.NoError(t, err)

		blocked, err := c.user.ListBlocked(alice.ctx(), &userv1.ListBlockedRequest{})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(blocked.Blocked), 1)

		// Unblock
		_, err = c.user.UnblockUser(alice.ctx(), &userv1.UnblockUserRequest{
			TargetUserId: charlie.ID,
		})
		require.NoError(t, err)
	})

	// ──────────────────────────────────────
	// 17. KICK + BAN: Moderation
	// ──────────────────────────────────────
	t.Run("17_Kick_And_Ban", func(t *testing.T) {
		// Alice kicks Charlie
		_, err := c.guild.KickMember(alice.ctx(), &guildv1.KickMemberRequest{
			GuildId: guildID, UserId: charlie.ID,
		})
		require.NoError(t, err)

		// Alice bans Charlie
		_, err = c.guild.BanMember(alice.ctx(), &guildv1.BanMemberRequest{
			GuildId: guildID, UserId: charlie.ID, Reason: "spamming",
		})
		require.NoError(t, err)

		// Unban
		_, err = c.guild.UnbanMember(alice.ctx(), &guildv1.UnbanMemberRequest{
			GuildId: guildID, UserId: charlie.ID,
		})
		require.NoError(t, err)
	})

	// ──────────────────────────────────────
	// 18. MESSAGE DELETE
	// ──────────────────────────────────────
	t.Run("18_Delete_Message", func(t *testing.T) {
		_, err := c.message.DeleteMessage(alice.ctx(), &messagev1.DeleteMessageRequest{
			ChannelId: textChannelID, MessageId: msgID,
		})
		require.NoError(t, err)
	})

	// ──────────────────────────────────────
	// 19. CHANNEL: Update and delete
	// ──────────────────────────────────────
	t.Run("19_Channel_Update_Delete", func(t *testing.T) {
		// Update channel topic
		_, err := c.channel.UpdateChannel(alice.ctx(), &channelv1.UpdateChannelRequest{
			ChannelId: textChannelID, Name: strPtr("general-chat"),
		})
		require.NoError(t, err)

		// Delete voice channel
		_, err = c.channel.DeleteChannel(alice.ctx(), &channelv1.DeleteChannelRequest{
			ChannelId: voiceChannelID,
		})
		require.NoError(t, err)
	})

	// ──────────────────────────────────────
	// 20. FRIEND REMOVE + GUILD DELETE + LOGOUT
	// ──────────────────────────────────────
	t.Run("20_Cleanup", func(t *testing.T) {
		// Remove friend
		_, err := c.user.RemoveFriend(alice.ctx(), &userv1.RemoveFriendRequest{
			FriendId: bob.ID,
		})
		require.NoError(t, err)

		// Bob leaves guild
		_, err = c.guild.LeaveGuild(bob.ctx(), &guildv1.LeaveGuildRequest{GuildId: guildID})
		require.NoError(t, err)

		// Alice deletes guild
		_, err = c.guild.DeleteGuild(alice.ctx(), &guildv1.DeleteGuildRequest{GuildId: guildID})
		require.NoError(t, err)

		// Logout
		_, err = c.auth.Logout(alice.ctx(), &authv1.LogoutRequest{
			RefreshToken: alice.RefreshToken,
		})
		require.NoError(t, err)
	})

	t.Log("All 20 e2e test steps passed!")
}
