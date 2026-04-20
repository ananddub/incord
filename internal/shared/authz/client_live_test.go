//go:build live

// Integration tests that run against a live Postgres. Excluded from
// default `go test` runs via the `live` build tag; enable with:
//
//     DATABASE_URL=postgres://ndiscord:ndiscord@localhost:5433/ndiscord?sslmode=disable \
//       go test -tags live ./internal/shared/authz/... -v
//
// The tests write to isolated rows (UUID-suffixed guild/user/role/
// channel) and clean up on teardown so they can share the developer's
// running database without interfering with real data.

package authz

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newLivePool dials the URL in $DATABASE_URL (default: the usual dev
// mapping on port 5433). Skips the whole file if the DB isn't reachable
// so the test harness stays green on boxes without Postgres.
func newLivePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://ndiscord:ndiscord@localhost:5433/ndiscord?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Skipf("no live Postgres (set DATABASE_URL): %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		t.Skipf("Postgres not reachable: %v", err)
	}
	return pool
}

// fixture carries the identifiers we create for one test run so the
// defer cleanup can wipe them regardless of which assertion failed.
type fixture struct {
	pool    *pgxpool.Pool
	ctx     context.Context
	ownerID string
	memberID string
	guildID  string
	channelID string
	everyoneRoleID string
	modRoleID string
}

// setupFixture creates: owner user, member user, guild (owner=owner),
// member row for both, @everyone role + baseline grants, one text
// channel under the guild, and one extra role "mods" (empty grants).
func setupFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{pool: newLivePool(t), ctx: context.Background()}

	newUUID := func() string { return uuid.New().String() }
	f.ownerID = newUUID()
	f.memberID = newUUID()
	f.guildID = newUUID()
	f.channelID = newUUID()
	f.modRoleID = newUUID()

	// Users (bare minimum to satisfy FK requirements if any — most
	// tables here have UUID FKs to users but no FK constraint, so
	// insert into users to keep other paths valid).
	for _, uid := range []string{f.ownerID, f.memberID} {
		_, err := f.pool.Exec(f.ctx, `
			INSERT INTO users (id, username, email, password_hash, display_name)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (id) DO NOTHING`,
			uid, "test_"+uid[:8], "test_"+uid[:8]+"@test.local", "x", "Test "+uid[:4])
		if err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}

	// Guild (owner=ownerID)
	_, err := f.pool.Exec(f.ctx, `
		INSERT INTO guilds (id, name, description, icon_url, owner_id)
		VALUES ($1, 'authz-live-test', '', '', $2)`,
		f.guildID, f.ownerID)
	if err != nil {
		t.Fatalf("seed guild: %v", err)
	}

	// Guild members
	for _, uid := range []string{f.ownerID, f.memberID} {
		_, err := f.pool.Exec(f.ctx, `
			INSERT INTO guild_members (guild_id, user_id, nickname)
			VALUES ($1, $2, '')`, f.guildID, uid)
		if err != nil {
			t.Fatalf("seed member: %v", err)
		}
	}

	// @everyone role with baseline permissions
	err = f.pool.QueryRow(f.ctx, `
		INSERT INTO roles (guild_id, name, color, position)
		VALUES ($1, '@everyone', '', 0) RETURNING id`,
		f.guildID).Scan(&f.everyoneRoleID)
	if err != nil {
		t.Fatalf("seed @everyone role: %v", err)
	}
	_, err = f.pool.Exec(f.ctx, `
		INSERT INTO role_permissions (role_id, permission_id)
		SELECT $1, p.id FROM permissions p
		 WHERE p.name IN (
		   'VIEW_CHANNELS','SEND_MESSAGES','READ_MESSAGE_HISTORY',
		   'ADD_REACTIONS','CONNECT','SPEAK')`, f.everyoneRoleID)
	if err != nil {
		t.Fatalf("seed @everyone grants: %v", err)
	}

	// Assign both users to @everyone (mirrors the normal join path)
	for _, uid := range []string{f.ownerID, f.memberID} {
		_, err := f.pool.Exec(f.ctx, `
			INSERT INTO role_members (role_id, user_id)
			VALUES ($1, $2)`, f.everyoneRoleID, uid)
		if err != nil {
			t.Fatalf("seed @everyone member: %v", err)
		}
	}

	// Mod role — empty grants, position above @everyone
	_, err = f.pool.Exec(f.ctx, `
		INSERT INTO roles (id, guild_id, name, color, position)
		VALUES ($1, $2, 'authz-live-mods', '#FF0000', 5)`,
		f.modRoleID, f.guildID)
	if err != nil {
		t.Fatalf("seed mod role: %v", err)
	}

	// Channel
	_, err = f.pool.Exec(f.ctx, `
		INSERT INTO channels (id, guild_id, name, type, topic, position)
		VALUES ($1, $2, 'authz-live-channel', 1, '', 0)`,
		f.channelID, f.guildID)
	if err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	return f
}

func (f *fixture) teardown(t *testing.T) {
	t.Helper()
	// DELETE in FK-safe order.
	_, _ = f.pool.Exec(f.ctx, `DELETE FROM channel_permission_overwrites WHERE channel_id = $1`, f.channelID)
	_, _ = f.pool.Exec(f.ctx, `DELETE FROM role_permissions WHERE role_id IN ($1, $2)`, f.everyoneRoleID, f.modRoleID)
	_, _ = f.pool.Exec(f.ctx, `DELETE FROM role_members WHERE role_id IN ($1, $2)`, f.everyoneRoleID, f.modRoleID)
	_, _ = f.pool.Exec(f.ctx, `DELETE FROM roles WHERE id IN ($1, $2)`, f.everyoneRoleID, f.modRoleID)
	_, _ = f.pool.Exec(f.ctx, `DELETE FROM channels WHERE id = $1`, f.channelID)
	_, _ = f.pool.Exec(f.ctx, `DELETE FROM guild_members WHERE guild_id = $1`, f.guildID)
	_, _ = f.pool.Exec(f.ctx, `DELETE FROM guilds WHERE id = $1`, f.guildID)
	_, _ = f.pool.Exec(f.ctx, `DELETE FROM users WHERE id IN ($1, $2)`, f.ownerID, f.memberID)
	f.pool.Close()
}

func TestCheck_OwnerBypass(t *testing.T) {
	f := setupFixture(t)
	defer f.teardown(t)
	c := NewClient(f.pool)

	for _, relation := range []string{"can_manage_guild", "can_kick", "can_ban", "can_manage_roles", "can_administrator"} {
		if relation == "can_administrator" {
			continue // no such relation in map
		}
		if !c.Check(f.ctx, UserKey(f.ownerID), relation, GuildKey(f.guildID)) {
			t.Errorf("owner denied %s (expected allow)", relation)
		}
	}
}

func TestCheck_MemberBaseline(t *testing.T) {
	f := setupFixture(t)
	defer f.teardown(t)
	c := NewClient(f.pool)

	// baseline @everyone perms → true
	for _, relation := range []string{"can_view_channels", "can_send_messages", "can_connect"} {
		if !c.Check(f.ctx, UserKey(f.memberID), relation, GuildKey(f.guildID)) {
			t.Errorf("member denied baseline %s (expected allow)", relation)
		}
	}
	// admin-tier perms → false
	for _, relation := range []string{"can_kick", "can_ban", "can_manage_guild"} {
		if c.Check(f.ctx, UserKey(f.memberID), relation, GuildKey(f.guildID)) {
			t.Errorf("member granted %s (expected deny)", relation)
		}
	}
}

func TestGrantRolePermission_FlipsCheck(t *testing.T) {
	f := setupFixture(t)
	defer f.teardown(t)
	c := NewClient(f.pool)

	// Add member to mod role
	if err := c.AddUserToRole(f.ctx, f.memberID, f.modRoleID); err != nil {
		t.Fatalf("AddUserToRole: %v", err)
	}
	// Pre-grant: member doesn't have KICK_MEMBERS
	if c.Check(f.ctx, UserKey(f.memberID), "can_kick", GuildKey(f.guildID)) {
		t.Fatal("member can_kick before grant — expected deny")
	}
	// Grant the role KICK_MEMBERS
	if err := c.GrantRolePermission(f.ctx, f.modRoleID, f.guildID, "can_kick"); err != nil {
		t.Fatalf("GrantRolePermission: %v", err)
	}
	if !c.Check(f.ctx, UserKey(f.memberID), "can_kick", GuildKey(f.guildID)) {
		t.Fatal("member can_kick after grant — expected allow")
	}
	// Revoke flips back
	if err := c.RevokeRolePermission(f.ctx, f.modRoleID, f.guildID, "can_kick"); err != nil {
		t.Fatalf("RevokeRolePermission: %v", err)
	}
	if c.Check(f.ctx, UserKey(f.memberID), "can_kick", GuildKey(f.guildID)) {
		t.Fatal("member can_kick after revoke — expected deny")
	}
}

func TestAdminPermissionBypassesEverything(t *testing.T) {
	f := setupFixture(t)
	defer f.teardown(t)
	c := NewClient(f.pool)

	_ = c.AddUserToRole(f.ctx, f.memberID, f.modRoleID)
	if err := c.GrantRolePermission(f.ctx, f.modRoleID, f.guildID, "admin"); err != nil {
		t.Fatalf("grant ADMINISTRATOR: %v", err)
	}

	for _, relation := range []string{"can_kick", "can_ban", "can_manage_guild", "can_manage_webhooks"} {
		if !c.Check(f.ctx, UserKey(f.memberID), relation, GuildKey(f.guildID)) {
			t.Errorf("admin-holder denied %s (expected allow)", relation)
		}
	}
}

func TestCanViewChannel_BaselineAndOverride(t *testing.T) {
	f := setupFixture(t)
	defer f.teardown(t)
	c := NewClient(f.pool)

	// Baseline: member has VIEW_CHANNELS via @everyone → channel view = true
	if !c.CanViewChannel(f.ctx, f.memberID, f.channelID) {
		t.Fatal("member baseline CanViewChannel = false; expected true")
	}

	// Install @everyone DENY VIEW_CHANNELS on the channel
	if err := c.SetChannelOverride(f.ctx, f.channelID, "role", f.everyoneRoleID, "can_view_channels", "deny"); err != nil {
		t.Fatalf("SetChannelOverride deny: %v", err)
	}
	if c.CanViewChannel(f.ctx, f.memberID, f.channelID) {
		t.Fatal("member CanViewChannel = true after @everyone deny; expected false")
	}

	// Owner bypasses overrides
	if !c.CanViewChannel(f.ctx, f.ownerID, f.channelID) {
		t.Fatal("owner CanViewChannel = false after @everyone deny; owner should bypass")
	}

	// User-specific allow on top of role deny → user wins
	if err := c.SetChannelOverride(f.ctx, f.channelID, "user", f.memberID, "can_view_channels", "allow"); err != nil {
		t.Fatalf("SetChannelOverride user allow: %v", err)
	}
	if !c.CanViewChannel(f.ctx, f.memberID, f.channelID) {
		t.Fatal("member CanViewChannel = false despite user allow override; user should win over role deny")
	}

	// Cleanup the overrides we installed (extra cleanup, teardown also handles it)
	_ = c.DeleteChannelOverride(f.ctx, f.channelID, "role", f.everyoneRoleID, "can_view_channels")
	_ = c.DeleteChannelOverride(f.ctx, f.channelID, "user", f.memberID, "can_view_channels")
}

func TestCanActOn_RoleHierarchy(t *testing.T) {
	f := setupFixture(t)
	defer f.teardown(t)
	c := NewClient(f.pool)

	// member in mod role; target = owner
	_ = c.AddUserToRole(f.ctx, f.memberID, f.modRoleID)

	// owner is always untouchable
	if c.CanActOn(f.ctx, f.memberID, f.ownerID, f.guildID) {
		t.Fatal("CanActOn member→owner = true; owner must always be refused")
	}

	// owner→member is always allowed
	if !c.CanActOn(f.ctx, f.ownerID, f.memberID, f.guildID) {
		t.Fatal("CanActOn owner→member = false; owner must always pass")
	}

	// Create third user with no roles — mod should outrank them
	lowID := uuid.New().String()
	_, err := f.pool.Exec(f.ctx, `
		INSERT INTO users (id, username, email, password_hash, display_name)
		VALUES ($1, $2, $3, 'x', 'Low')`,
		lowID, "low_"+lowID[:8], "low_"+lowID[:8]+"@test.local")
	if err != nil {
		t.Fatalf("seed low user: %v", err)
	}
	defer f.pool.Exec(f.ctx, `DELETE FROM users WHERE id = $1`, lowID)
	_, _ = f.pool.Exec(f.ctx, `INSERT INTO guild_members (guild_id, user_id, nickname) VALUES ($1, $2, '')`, f.guildID, lowID)
	_, _ = f.pool.Exec(f.ctx, `INSERT INTO role_members (role_id, user_id) VALUES ($1, $2)`, f.everyoneRoleID, lowID)
	defer f.pool.Exec(f.ctx, `DELETE FROM role_members WHERE user_id = $1`, lowID)
	defer f.pool.Exec(f.ctx, `DELETE FROM guild_members WHERE user_id = $1`, lowID)

	if !c.CanActOn(f.ctx, f.memberID, lowID, f.guildID) {
		t.Fatal("mod cannot act on @everyone-only user; role-position check failed")
	}
}

func TestListGuildPermissions(t *testing.T) {
	f := setupFixture(t)
	defer f.teardown(t)
	c := NewClient(f.pool)

	// Owner gets the whole catalogue
	ownerPerms, err := c.ListGuildPermissions(f.ctx, f.ownerID, f.guildID)
	if err != nil {
		t.Fatalf("ListGuildPermissions(owner): %v", err)
	}
	if len(ownerPerms) < 40 {
		t.Fatalf("owner perm count %d < 40; expected full catalogue", len(ownerPerms))
	}

	// Member gets just the baseline (6 perms seeded in this fixture)
	memberPerms, err := c.ListGuildPermissions(f.ctx, f.memberID, f.guildID)
	if err != nil {
		t.Fatalf("ListGuildPermissions(member): %v", err)
	}
	want := map[string]bool{"VIEW_CHANNELS": true, "SEND_MESSAGES": true, "READ_MESSAGE_HISTORY": true, "ADD_REACTIONS": true, "CONNECT": true, "SPEAK": true}
	if len(memberPerms) != len(want) {
		t.Fatalf("member perms = %v; want %d entries from %v", memberPerms, len(want), keys(want))
	}
	for _, p := range memberPerms {
		if !want[p] {
			t.Errorf("unexpected member perm %s", p)
		}
	}
}

func TestListChannelOverrides_Roundtrip(t *testing.T) {
	f := setupFixture(t)
	defer f.teardown(t)
	c := NewClient(f.pool)

	// Pre-existing: empty
	got, err := c.ListChannelOverrides(f.ctx, f.channelID)
	if err != nil {
		t.Fatalf("ListChannelOverrides: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 overrides, got %d", len(got))
	}

	// Install two
	if err := c.SetChannelOverride(f.ctx, f.channelID, "role", f.everyoneRoleID, "can_send_messages", "deny"); err != nil {
		t.Fatalf("%v", err)
	}
	if err := c.SetChannelOverride(f.ctx, f.channelID, "user", f.memberID, "can_send_messages", "allow"); err != nil {
		t.Fatalf("%v", err)
	}

	got, _ = c.ListChannelOverrides(f.ctx, f.channelID)
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d: %+v", len(got), got)
	}

	// Pretty dump for the log so the shape is easy to eyeball
	for _, o := range got {
		fmt.Printf("  %s %s → %s on %s\n", o.TargetType, o.TargetID[:8], o.Effect, o.Permission)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
