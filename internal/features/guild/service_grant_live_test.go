//go:build live

// Integration tests for GrantRolePermission / RevokeRolePermission on the
// guild.Service layer. These hit a live Postgres (set $DATABASE_URL) so the
// full check path runs — CanManageRoles + the self-has anti-escalation
// check + the actual SQL grant/revoke on role_permissions.
//
// Enable with:
//   DATABASE_URL=postgres://ndiscord:ndiscord@localhost:5433/ndiscord?sslmode=disable \
//     go test -tags live -count=1 -run TestGrantRolePermission ./internal/features/guild/...

package guild

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ananddub/ndiscord_backend/internal/shared/authz"
)

// liveSetup spins up Repository + Service wired to real Postgres + authz.
// Returns owner, moderator (MANAGE_ROLES only), regular member, plus the
// guild id and a moderator role to target with grants. Cleans up on
// t.Cleanup so each test starts clean.
type liveHarness struct {
	ctx     context.Context
	pool    *pgxpool.Pool
	svc     *Service
	authz   *authz.Client
	ownerID pgtype.UUID
	modID   pgtype.UUID // holds MANAGE_ROLES only, nothing else
	memID   pgtype.UUID // plain @everyone member
	guildID pgtype.UUID
	modRole pgtype.UUID // role we'll grant/revoke perms on
	modUser pgtype.UUID // user-role that `mod` is assigned to (has MANAGE_ROLES)
}

func setupLiveHarness(t *testing.T) *liveHarness {
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
	ctx := context.Background()
	h := &liveHarness{ctx: ctx, pool: pool, authz: authz.NewClient(pool)}

	strToUUID := func(s string) pgtype.UUID {
		var u pgtype.UUID
		_ = u.Scan(s)
		return u
	}
	h.ownerID = strToUUID(uuid.New().String())
	h.modID = strToUUID(uuid.New().String())
	h.memID = strToUUID(uuid.New().String())
	h.guildID = strToUUID(uuid.New().String())
	h.modRole = strToUUID(uuid.New().String())
	h.modUser = strToUUID(uuid.New().String())

	// Seed three users.
	for _, u := range []pgtype.UUID{h.ownerID, h.modID, h.memID} {
		_, err := pool.Exec(ctx, `
			INSERT INTO users (id, username, email, password_hash, display_name)
			VALUES ($1, $2, $3, 'x', 'T')`,
			u, "u_"+uuid.New().String()[:8], "u_"+uuid.New().String()[:8]+"@test.local")
		if err != nil {
			t.Fatalf("seed user: %v", err)
		}
	}

	// Guild owned by owner.
	_, err = pool.Exec(ctx, `
		INSERT INTO guilds (id, name, description, icon_url, owner_id)
		VALUES ($1, 'grant-test', '', '', $2)`, h.guildID, h.ownerID)
	if err != nil {
		t.Fatalf("seed guild: %v", err)
	}

	// Membership for all three users.
	for _, u := range []pgtype.UUID{h.ownerID, h.modID, h.memID} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO guild_members (guild_id, user_id, nickname) VALUES ($1, $2, '')`,
			h.guildID, u); err != nil {
			t.Fatalf("seed guild_member: %v", err)
		}
	}

	// @everyone role with baseline perms, both non-owners assigned.
	var everyoneID pgtype.UUID
	err = pool.QueryRow(ctx, `
		INSERT INTO roles (guild_id, name, color, position)
		VALUES ($1, '@everyone', '', 0) RETURNING id`, h.guildID).Scan(&everyoneID)
	if err != nil {
		t.Fatalf("seed @everyone: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO role_permissions (role_id, permission_id)
		SELECT $1, p.id FROM permissions p
		 WHERE p.name IN ('VIEW_CHANNELS','SEND_MESSAGES','READ_MESSAGE_HISTORY')`, everyoneID)
	if err != nil {
		t.Fatalf("seed everyone grants: %v", err)
	}
	for _, u := range []pgtype.UUID{h.modID, h.memID} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO role_members (role_id, user_id) VALUES ($1, $2)`,
			everyoneID, u); err != nil {
			t.Fatalf("seed everyone_member: %v", err)
		}
	}

	// "mod-user" role — holds MANAGE_ROLES only, nothing else. Mod user
	// belongs to it. This is the escalation-attempt identity: has the
	// permission to edit roles but should not be able to grant perms
	// they personally lack.
	_, err = pool.Exec(ctx, `
		INSERT INTO roles (id, guild_id, name, color, position)
		VALUES ($1, $2, 'mods-with-manage-roles', '#00FF00', 10)`, h.modUser, h.guildID)
	if err != nil {
		t.Fatalf("seed mod-user role: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO role_permissions (role_id, permission_id)
		SELECT $1, p.id FROM permissions p WHERE p.name = 'MANAGE_ROLES'`, h.modUser)
	if err != nil {
		t.Fatalf("seed mod-user MANAGE_ROLES: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO role_members (role_id, user_id) VALUES ($1, $2)`,
		h.modUser, h.modID); err != nil {
		t.Fatalf("seed mod-user membership: %v", err)
	}

	// Target role that we'll grant/revoke permissions on.
	_, err = pool.Exec(ctx, `
		INSERT INTO roles (id, guild_id, name, color, position)
		VALUES ($1, $2, 'target', '#FF0000', 5)`, h.modRole, h.guildID)
	if err != nil {
		t.Fatalf("seed target role: %v", err)
	}

	// Wire the guild service — repo + authz, nats nil (events best-effort).
	repo := NewRepository(pool, nil)
	h.svc = NewService(repo, nil, h.authz)

	t.Cleanup(func() {
		// FK-safe teardown.
		_, _ = pool.Exec(ctx, `DELETE FROM role_permissions WHERE role_id IN ($1,$2,$3)`, everyoneID, h.modUser, h.modRole)
		_, _ = pool.Exec(ctx, `DELETE FROM role_members WHERE role_id IN ($1,$2,$3)`, everyoneID, h.modUser, h.modRole)
		_, _ = pool.Exec(ctx, `DELETE FROM roles WHERE id IN ($1,$2,$3)`, everyoneID, h.modUser, h.modRole)
		_, _ = pool.Exec(ctx, `DELETE FROM guild_members WHERE guild_id = $1`, h.guildID)
		_, _ = pool.Exec(ctx, `DELETE FROM guilds WHERE id = $1`, h.guildID)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2,$3)`, h.ownerID, h.modID, h.memID)
		pool.Close()
	})
	return h
}

// --- Positive cases: owner + admin can do anything ---

func TestGrantRolePermission_OwnerCanGrantAnything(t *testing.T) {
	h := setupLiveHarness(t)
	for _, perm := range []string{"KICK_MEMBERS", "BAN_MEMBERS", "ADMINISTRATOR", "MANAGE_GUILD"} {
		if err := h.svc.GrantRolePermission(h.ctx, h.ownerID, h.guildID, h.modRole, perm); err != nil {
			t.Errorf("owner grant %s: unexpected error: %v", perm, err)
		}
	}
}

func TestGrantRolePermission_AdminCanGrantAnything(t *testing.T) {
	h := setupLiveHarness(t)
	// Promote mod-user role to ADMINISTRATOR so h.modID becomes admin.
	if _, err := h.pool.Exec(h.ctx, `
		INSERT INTO role_permissions (role_id, permission_id)
		SELECT $1, p.id FROM permissions p WHERE p.name = 'ADMINISTRATOR'`, h.modUser); err != nil {
		t.Fatalf("promote mod-user to admin: %v", err)
	}
	for _, perm := range []string{"KICK_MEMBERS", "BAN_MEMBERS", "MANAGE_WEBHOOKS"} {
		if err := h.svc.GrantRolePermission(h.ctx, h.modID, h.guildID, h.modRole, perm); err != nil {
			t.Errorf("admin grant %s: unexpected error: %v", perm, err)
		}
	}
}

// --- Negative cases: caller lacks the permission they're trying to grant ---

func TestGrantRolePermission_CannotGrantPermYouDontHave(t *testing.T) {
	h := setupLiveHarness(t)
	// mod has MANAGE_ROLES but NOT KICK_MEMBERS — should be denied.
	err := h.svc.GrantRolePermission(h.ctx, h.modID, h.guildID, h.modRole, "KICK_MEMBERS")
	if !errors.Is(err, ErrInsufficientPermissions) {
		t.Fatalf("expected ErrInsufficientPermissions, got: %v", err)
	}
	// Verify nothing was actually written.
	var cnt int
	_ = h.pool.QueryRow(h.ctx, `
		SELECT COUNT(*) FROM role_permissions rp
		JOIN permissions p ON p.id = rp.permission_id
		WHERE rp.role_id = $1 AND p.name = 'KICK_MEMBERS'`, h.modRole).Scan(&cnt)
	if cnt != 0 {
		t.Fatalf("grant wrote %d rows despite denial", cnt)
	}
}

// The escalation scenario the fix exists to prevent: moderator with only
// MANAGE_ROLES tries to grant ADMINISTRATOR to any role. Must be blocked.
func TestGrantRolePermission_ModeratorCannotEscalateToAdmin(t *testing.T) {
	h := setupLiveHarness(t)
	err := h.svc.GrantRolePermission(h.ctx, h.modID, h.guildID, h.modRole, "ADMINISTRATOR")
	if !errors.Is(err, ErrInsufficientPermissions) {
		t.Fatalf("PRIVILEGE ESCALATION: moderator granted ADMINISTRATOR, err=%v", err)
	}
}

// Moderator who HAS the permission they're granting — should succeed.
// (This isolates the self-has check from the CanManageRoles gate.)
func TestGrantRolePermission_ModeratorCanGrantPermTheyHold(t *testing.T) {
	h := setupLiveHarness(t)
	// Give mod-user KICK_MEMBERS so mod personally holds it.
	if _, err := h.pool.Exec(h.ctx, `
		INSERT INTO role_permissions (role_id, permission_id)
		SELECT $1, p.id FROM permissions p WHERE p.name = 'KICK_MEMBERS'`, h.modUser); err != nil {
		t.Fatalf("give mod KICK_MEMBERS: %v", err)
	}
	if err := h.svc.GrantRolePermission(h.ctx, h.modID, h.guildID, h.modRole, "KICK_MEMBERS"); err != nil {
		t.Fatalf("expected grant to succeed when caller holds the perm, got: %v", err)
	}
	// Now the target role should actually have KICK_MEMBERS.
	var cnt int
	_ = h.pool.QueryRow(h.ctx, `
		SELECT COUNT(*) FROM role_permissions rp
		JOIN permissions p ON p.id = rp.permission_id
		WHERE rp.role_id = $1 AND p.name = 'KICK_MEMBERS'`, h.modRole).Scan(&cnt)
	if cnt != 1 {
		t.Fatalf("expected 1 KICK_MEMBERS grant on target role, got %d", cnt)
	}
}

// Plain member with no MANAGE_ROLES → fails at the first gate regardless
// of which perm they ask to grant.
func TestGrantRolePermission_PlainMemberAlwaysDenied(t *testing.T) {
	h := setupLiveHarness(t)
	err := h.svc.GrantRolePermission(h.ctx, h.memID, h.guildID, h.modRole, "SEND_MESSAGES")
	if !errors.Is(err, ErrInsufficientPermissions) {
		t.Fatalf("plain member grant should fail CanManageRoles, got: %v", err)
	}
}

// --- Revoke mirrors Grant: same anti-escalation rule applies ---

func TestRevokeRolePermission_CannotRevokePermYouDontHave(t *testing.T) {
	h := setupLiveHarness(t)
	// Pre-seed: target role has KICK_MEMBERS. Mod (MANAGE_ROLES only)
	// tries to revoke it — must be denied.
	if _, err := h.pool.Exec(h.ctx, `
		INSERT INTO role_permissions (role_id, permission_id)
		SELECT $1, p.id FROM permissions p WHERE p.name = 'KICK_MEMBERS'`, h.modRole); err != nil {
		t.Fatalf("pre-seed target KICK_MEMBERS: %v", err)
	}
	err := h.svc.RevokeRolePermission(h.ctx, h.modID, h.guildID, h.modRole, "KICK_MEMBERS")
	if !errors.Is(err, ErrInsufficientPermissions) {
		t.Fatalf("expected revoke denied, got: %v", err)
	}
	// Still there.
	var cnt int
	_ = h.pool.QueryRow(h.ctx, `
		SELECT COUNT(*) FROM role_permissions rp
		JOIN permissions p ON p.id = rp.permission_id
		WHERE rp.role_id = $1 AND p.name = 'KICK_MEMBERS'`, h.modRole).Scan(&cnt)
	if cnt != 1 {
		t.Fatalf("revoke wrote through despite denial (rows=%d)", cnt)
	}
}

func TestRevokeRolePermission_OwnerCanAlwaysRevoke(t *testing.T) {
	h := setupLiveHarness(t)
	if _, err := h.pool.Exec(h.ctx, `
		INSERT INTO role_permissions (role_id, permission_id)
		SELECT $1, p.id FROM permissions p WHERE p.name = 'ADMINISTRATOR'`, h.modRole); err != nil {
		t.Fatalf("seed target ADMINISTRATOR: %v", err)
	}
	if err := h.svc.RevokeRolePermission(h.ctx, h.ownerID, h.guildID, h.modRole, "ADMINISTRATOR"); err != nil {
		t.Fatalf("owner revoke ADMINISTRATOR failed: %v", err)
	}
}

// --- Sanity: invalid permission name still rejected with ErrInvalidPermission ---

func TestGrantRolePermission_RejectsUnknownPermission(t *testing.T) {
	h := setupLiveHarness(t)
	err := h.svc.GrantRolePermission(h.ctx, h.ownerID, h.guildID, h.modRole, "TOTALLY_BOGUS_PERMISSION")
	if !errors.Is(err, ErrInvalidPermission) {
		t.Fatalf("expected ErrInvalidPermission, got: %v", err)
	}
}
