package realtime

func GuildChannelMessage(guildID, channelID string) string {
	return "guild." + guildID + ".channel.message." + channelID
}

// Guild channel typing
func GuildChannelTyping(guildID, channelID string) string {
	return "guild." + guildID + ".channel.typing." + channelID
}

// Guild voice state
func GuildChannelVoice(guildID, channelID string) string {
	return "guild." + guildID + ".channel.voice." + channelID
}

// Guild voice text chat
func GuildChannelVoiceChat(guildID, channelID string) string {
	return "guild." + guildID + ".channel.voicechat." + channelID
}

// Guild events
func GuildEvents(guildID string) string {
	return "guild." + guildID + ".events"
}

// DM message
func DmMessage(userID, channelID string) string {
	return "dm." + userID + ".message." + channelID
}

// DM channel lifecycle (create/update/delete/member changes) for a given user.
func DmChannels(userID string) string {
	return "dm." + userID + ".channels"
}

// DM voice-call signalling (incoming, accepted, rejected, ended, participant
// join/leave) targeted at a specific user.
func DmCall(userID string) string {
	return "dm." + userID + ".call"
}

// DM typing
func DmTyping(userID, channelID string) string {
	return "dm." + userID + ".typing." + channelID
}

// Friend activity
func FriendActivity(userID string) string {
	return "friend." + userID + ".activity"
}

// ── Wildcard subjects (subscribe - all channels at once) ──

// All text messages in a guild
func GuildAllMessages(guildID string) string {
	return "guild." + guildID + ".channel.message.*"
}

// All typing in a guild
func GuildAllTyping(guildID string) string {
	return "guild." + guildID + ".channel.typing.*"
}

// All voice state in a guild
func GuildAllVoice(guildID string) string {
	return "guild." + guildID + ".channel.voice.*"
}

// All voice chat in a guild
func GuildAllVoiceChat(guildID string) string {
	return "guild." + guildID + ".channel.voicechat.*"
}

// All DM messages for a user
func DmAllMessages(userID string) string {
	return "dm." + userID + ".message.*"
}

// All DM typing for a user
func DmAllTyping(userID string) string {
	return "dm." + userID + ".typing.*"
}
