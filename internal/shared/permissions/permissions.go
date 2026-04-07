package permissions

// Permission bitfield constants - same approach as Discord.
// Each permission is a bit in a int64 bitfield.
const (
	ViewChannels       int64 = 1 << 0  // Read messages in text channels, see voice channels
	SendMessages       int64 = 1 << 1  // Send messages in text channels
	ManageMessages     int64 = 1 << 2  // Delete/pin others' messages
	ManageChannels     int64 = 1 << 3  // Create/edit/delete channels
	ManageGuild        int64 = 1 << 4  // Edit guild name, icon, settings
	KickMembers        int64 = 1 << 5  // Kick members from guild
	BanMembers         int64 = 1 << 6  // Ban members from guild
	ManageRoles        int64 = 1 << 7  // Create/edit/delete roles, assign roles
	ManageInvites      int64 = 1 << 8  // Create/delete invites
	AddReactions       int64 = 1 << 9  // Add reactions to messages
	Connect            int64 = 1 << 10 // Connect to voice channels
	Speak              int64 = 1 << 11 // Speak in voice channels
	Stream             int64 = 1 << 12 // Screen share / video
	MuteMembers        int64 = 1 << 13 // Mute others in voice
	DeafenMembers      int64 = 1 << 14 // Deafen others in voice
	MentionEveryone    int64 = 1 << 15 // @everyone, @here
	ManageEmojis       int64 = 1 << 16 // Add/remove custom emojis
	ManageWebhooks     int64 = 1 << 17 // Create/edit/delete webhooks
	Administrator      int64 = 1 << 31 // Bypasses all permission checks

	// Common presets
	AllPermissions     int64 = 0x7FFFFFFFFFFFFFFF
	DefaultPermissions int64 = ViewChannels | SendMessages | AddReactions | Connect | Speak | Stream
)

// Has checks if a permission set includes the given permission.
func Has(userPerms, perm int64) bool {
	if userPerms&Administrator != 0 {
		return true // admin bypasses all
	}
	return userPerms&perm == perm
}

// HasAny checks if a permission set includes any of the given permissions.
func HasAny(userPerms int64, perms ...int64) bool {
	if userPerms&Administrator != 0 {
		return true
	}
	for _, p := range perms {
		if userPerms&p == p {
			return true
		}
	}
	return false
}
