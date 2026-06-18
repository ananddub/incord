package message

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ananddub/ndiscord_backend/gen/scylladb"
	"github.com/gocql/gocql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// setupScylla starts a ScyllaDB container, creates the keyspace and tables,
// and returns a connected *gocql.Session. The container is terminated when the
// test (and its subtests) finish.
func setupScylla(t *testing.T) *gocql.Session {
	t.Helper()
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "scylladb/scylla:latest",
		ExposedPorts: []string{"9042/tcp"},
		Cmd:          []string{"--smp", "1", "--memory", "512M", "--overprovisioned", "1"},
		WaitingFor:   wait.ForLog("init - serving").WithStartupTimeout(120 * time.Second),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "failed to start scylla container")

	host, err := container.Host(ctx)
	require.NoError(t, err)
	mappedPort, err := container.MappedPort(ctx, "9042")
	require.NoError(t, err)

	// Connect without keyspace first to create it.
	cluster := gocql.NewCluster(fmt.Sprintf("%s:%s", host, mappedPort.Port()))
	cluster.Consistency = gocql.Quorum
	cluster.Timeout = 30 * time.Second
	cluster.ConnectTimeout = 30 * time.Second

	// Retry connection a few times – the CQL port may accept TCP before it
	// is truly ready to serve queries.
	var initSession *gocql.Session
	for i := 0; i < 20; i++ {
		initSession, err = cluster.CreateSession()
		if err == nil {
			break
		}
		time.Sleep(2 * time.Second)
	}
	require.NoError(t, err, "failed to connect to scylla")

	// Create keyspace.
	err = initSession.Query(`
		CREATE KEYSPACE IF NOT EXISTS ndiscord_test
		WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1}
	`).Exec()
	require.NoError(t, err, "failed to create keyspace")

	// Create tables inside the keyspace. Kept in sync with
	// db/scylla/migrations/000001_init.cql — any new column/table there
	// needs to be mirrored here for the test harness.
	tables := []string{
		`CREATE TABLE IF NOT EXISTS ndiscord_test.messages (
			channel_id UUID,
			id         TIMEUUID,
			author_id  UUID,
			content    TEXT,
			type       INT,
			reply_to_id UUID,
			pinned     BOOLEAN,
			deleted    BOOLEAN,
			edited_at  TIMESTAMP,
			updated_at TIMESTAMP,
			created_at TIMESTAMP,
			forwarded_from_channel_id UUID,
			forwarded_from_message_id UUID,
			forwarded_from_author_id  UUID,
			mention_user_ids SET<UUID>,
			PRIMARY KEY (channel_id, id)
		) WITH CLUSTERING ORDER BY (id DESC)`,

		`CREATE TABLE IF NOT EXISTS ndiscord_test.message_attachments (
			channel_id   UUID,
			message_id   TIMEUUID,
			id           UUID,
			filename     TEXT,
			url          TEXT,
			content_type TEXT,
			size         BIGINT,
			PRIMARY KEY ((channel_id, message_id), id)
		)`,

		`CREATE TABLE IF NOT EXISTS ndiscord_test.message_reactions (
			channel_id UUID,
			message_id TIMEUUID,
			emoji      TEXT,
			user_id    UUID,
			PRIMARY KEY ((channel_id, message_id), emoji, user_id)
		)`,

		`CREATE TABLE IF NOT EXISTS ndiscord_test.read_states (
			user_id             UUID,
			channel_id          UUID,
			last_read_message_id TIMEUUID,
			mention_count       INT,
			PRIMARY KEY (user_id, channel_id)
		)`,

		`CREATE TABLE IF NOT EXISTS ndiscord_test.message_edit_history (
			channel_id  UUID,
			message_id  TIMEUUID,
			old_content TEXT,
			edited_at   TIMESTAMP,
			PRIMARY KEY ((channel_id, message_id), edited_at)
		) WITH CLUSTERING ORDER BY (edited_at DESC)`,
	}
	for _, ddl := range tables {
		require.NoError(t, initSession.Query(ddl).Exec(), "failed to run DDL")
	}
	initSession.Close()

	// Re-connect with the keyspace set.
	cluster.Keyspace = "ndiscord_test"
	session, err := cluster.CreateSession()
	require.NoError(t, err, "failed to connect to scylla with keyspace")

	t.Cleanup(func() {
		session.Close()
		_ = container.Terminate(ctx)
	})

	return session
}

// newTestRepo is a small helper that spins up Scylla and returns a Repository.
func newTestRepo(t *testing.T) *Repository {
	t.Helper()
	session := setupScylla(t)
	return NewRepository(session)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func makeMessage(channelID, authorID gocql.UUID, content string) *scylladb.CreatemessageParams {
	return &scylladb.CreatemessageParams{
		ChannelId: channelID,
		Id:        gocql.TimeUUID(),
		AuthorId:  authorID,
		Content:   content,
		Type:      0,
		Pinned:    false,
		CreatedAt: time.Now().Truncate(time.Millisecond),
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestCreateAndGetMessage(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	channelID := gocql.MustRandomUUID()
	authorID := gocql.MustRandomUUID()
	msg := makeMessage(channelID, authorID, "hello world")

	err := repo.CreateMessage(ctx, msg)
	require.NoError(t, err)

	got, err := repo.GetMessage(ctx, channelID, msg.Id)
	require.NoError(t, err)
	assert.Equal(t, msg.ChannelId, uuidValue(got.ChannelId))
	assert.Equal(t, msg.Id, uuidValue(got.Id))
	assert.Equal(t, msg.AuthorId, uuidValue(got.AuthorId))
	assert.Equal(t, msg.Content, stringValue(got.Content))
	assert.Equal(t, msg.Type, int64Value(got.Type))
	assert.Equal(t, msg.Pinned, boolValue(got.Pinned))
}

func TestGetMessage_NotFound(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	_, err := repo.GetMessage(ctx, gocql.MustRandomUUID(), gocql.TimeUUID())
	assert.Error(t, err)
}

func TestUpdateMessageContent(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	channelID := gocql.MustRandomUUID()
	authorID := gocql.MustRandomUUID()
	msg := makeMessage(channelID, authorID, "original")

	require.NoError(t, repo.CreateMessage(ctx, msg))

	editedAt := time.Now().Truncate(time.Millisecond)
	err := repo.UpdateMessageContent(ctx, channelID, msg.Id, "updated", editedAt)
	require.NoError(t, err)

	got, err := repo.GetMessage(ctx, channelID, msg.Id)
	require.NoError(t, err)
	assert.Equal(t, "updated", stringValue(got.Content))
	require.NotNil(t, got.EditedAt)
	// ScyllaDB timestamps are millisecond-precision.
	assert.WithinDuration(t, editedAt, *got.EditedAt, time.Millisecond)
}

func TestDeleteMessage(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	channelID := gocql.MustRandomUUID()
	authorID := gocql.MustRandomUUID()
	msg := makeMessage(channelID, authorID, "to be deleted")

	require.NoError(t, repo.CreateMessage(ctx, msg))

	err := repo.DeleteMessage(ctx, channelID, msg.Id)
	require.NoError(t, err)

	_, err = repo.GetMessage(ctx, channelID, msg.Id)
	assert.Error(t, err, "expected not found after delete")
}

func TestListMessages_DefaultOrder(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	channelID := gocql.MustRandomUUID()
	authorID := gocql.MustRandomUUID()

	// Insert 5 messages with a small gap so TimeUUIDs are distinct.
	ids := make([]gocql.UUID, 5)
	for i := 0; i < 5; i++ {
		msg := makeMessage(channelID, authorID, fmt.Sprintf("msg-%d", i))
		ids[i] = msg.Id
		require.NoError(t, repo.CreateMessage(ctx, msg))
		time.Sleep(5 * time.Millisecond) // ensure distinct TimeUUIDs
	}

	// Default listing (no cursor): newest first.
	msgs, err := repo.ListMessages(ctx, channelID, nil, nil, 10)
	require.NoError(t, err)
	require.Len(t, msgs, 5)

	// Should be in descending order of id (newest first).
	for i := 1; i < len(msgs); i++ {
		assert.True(t, uuidValue(msgs[i-1].Id).Time().After(uuidValue(msgs[i].Id).Time()) ||
			uuidValue(msgs[i-1].Id).Time().Equal(uuidValue(msgs[i].Id).Time()),
			"messages should be ordered newest first")
	}
}

func TestListMessages_Limit(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	channelID := gocql.MustRandomUUID()
	authorID := gocql.MustRandomUUID()

	for i := 0; i < 5; i++ {
		msg := makeMessage(channelID, authorID, fmt.Sprintf("msg-%d", i))
		require.NoError(t, repo.CreateMessage(ctx, msg))
		time.Sleep(5 * time.Millisecond)
	}

	msgs, err := repo.ListMessages(ctx, channelID, nil, nil, 3)
	require.NoError(t, err)
	assert.Len(t, msgs, 3)
}

func TestListMessages_BeforeCursor(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	channelID := gocql.MustRandomUUID()
	authorID := gocql.MustRandomUUID()

	ids := make([]gocql.UUID, 5)
	for i := 0; i < 5; i++ {
		msg := makeMessage(channelID, authorID, fmt.Sprintf("msg-%d", i))
		ids[i] = msg.Id
		require.NoError(t, repo.CreateMessage(ctx, msg))
		time.Sleep(5 * time.Millisecond)
	}

	// "before" the newest message should return the 4 older ones.
	cursor := ids[4] // newest
	msgs, err := repo.ListMessages(ctx, channelID, &cursor, nil, 10)
	require.NoError(t, err)
	assert.Len(t, msgs, 4)
	for _, m := range msgs {
		assert.True(t, uuidValue(m.Id).Time().Before(cursor.Time()),
			"all returned messages should be older than cursor")
	}
}

func TestListMessages_AfterCursor(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	channelID := gocql.MustRandomUUID()
	authorID := gocql.MustRandomUUID()

	ids := make([]gocql.UUID, 5)
	for i := 0; i < 5; i++ {
		msg := makeMessage(channelID, authorID, fmt.Sprintf("msg-%d", i))
		ids[i] = msg.Id
		require.NoError(t, repo.CreateMessage(ctx, msg))
		time.Sleep(5 * time.Millisecond)
	}

	// "after" the oldest message should return the 4 newer ones.
	cursor := ids[0] // oldest
	msgs, err := repo.ListMessages(ctx, channelID, nil, &cursor, 10)
	require.NoError(t, err)
	assert.Len(t, msgs, 4)
	for _, m := range msgs {
		assert.True(t, uuidValue(m.Id).Time().After(cursor.Time()),
			"all returned messages should be newer than cursor")
	}
}

func TestSetPinned(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	channelID := gocql.MustRandomUUID()
	authorID := gocql.MustRandomUUID()
	msg := makeMessage(channelID, authorID, "pin me")

	require.NoError(t, repo.CreateMessage(ctx, msg))

	// Pin the message.
	err := repo.SetPinned(ctx, channelID, msg.Id, true)
	require.NoError(t, err)

	got, err := repo.GetMessage(ctx, channelID, msg.Id)
	require.NoError(t, err)
	assert.True(t, boolValue(got.Pinned))

	// Unpin the message.
	err = repo.SetPinned(ctx, channelID, msg.Id, false)
	require.NoError(t, err)

	got, err = repo.GetMessage(ctx, channelID, msg.Id)
	require.NoError(t, err)
	assert.False(t, boolValue(got.Pinned))
}

func TestAddAndGetReactions(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	channelID := gocql.MustRandomUUID()
	authorID := gocql.MustRandomUUID()
	msg := makeMessage(channelID, authorID, "react to me")
	require.NoError(t, repo.CreateMessage(ctx, msg))

	user1 := gocql.MustRandomUUID()
	user2 := gocql.MustRandomUUID()

	// Two users react with the same emoji, user1 also adds a different one.
	require.NoError(t, repo.AddReaction(ctx, channelID, msg.Id, "thumbsup", user1))
	require.NoError(t, repo.AddReaction(ctx, channelID, msg.Id, "thumbsup", user2))
	require.NoError(t, repo.AddReaction(ctx, channelID, msg.Id, "heart", user1))

	// Get reactions from user1's perspective.
	reactions, err := repo.GetReactions(ctx, channelID, msg.Id, user1)
	require.NoError(t, err)
	assert.Len(t, reactions, 2)

	byEmoji := make(map[string]ReactionCount)
	for _, r := range reactions {
		byEmoji[r.Emoji] = r
	}

	thumbsup := byEmoji["thumbsup"]
	assert.Equal(t, int32(2), thumbsup.Count)
	assert.True(t, thumbsup.Me, "user1 reacted with thumbsup")

	heart := byEmoji["heart"]
	assert.Equal(t, int32(1), heart.Count)
	assert.True(t, heart.Me, "user1 reacted with heart")

	// Get reactions from user2's perspective.
	reactions2, err := repo.GetReactions(ctx, channelID, msg.Id, user2)
	require.NoError(t, err)

	byEmoji2 := make(map[string]ReactionCount)
	for _, r := range reactions2 {
		byEmoji2[r.Emoji] = r
	}
	assert.True(t, byEmoji2["thumbsup"].Me, "user2 reacted with thumbsup")
	assert.False(t, byEmoji2["heart"].Me, "user2 did NOT react with heart")
}

func TestRemoveReaction(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	channelID := gocql.MustRandomUUID()
	authorID := gocql.MustRandomUUID()
	msg := makeMessage(channelID, authorID, "remove reaction test")
	require.NoError(t, repo.CreateMessage(ctx, msg))

	user := gocql.MustRandomUUID()
	require.NoError(t, repo.AddReaction(ctx, channelID, msg.Id, "fire", user))

	// Verify it exists.
	reactions, err := repo.GetReactions(ctx, channelID, msg.Id, user)
	require.NoError(t, err)
	require.Len(t, reactions, 1)

	// Remove.
	err = repo.RemoveReaction(ctx, channelID, msg.Id, "fire", user)
	require.NoError(t, err)

	// Should be gone.
	reactions, err = repo.GetReactions(ctx, channelID, msg.Id, user)
	require.NoError(t, err)
	assert.Empty(t, reactions)
}

func TestUpsertReadState(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	userID := gocql.MustRandomUUID()
	channelID := gocql.MustRandomUUID()
	msgID := gocql.TimeUUID()

	err := repo.UpsertReadState(ctx, userID, channelID, msgID)
	require.NoError(t, err)

	// Upsert again with a newer message – should not error.
	msgID2 := gocql.TimeUUID()
	err = repo.UpsertReadState(ctx, userID, channelID, msgID2)
	require.NoError(t, err)
}

func TestListMessages_EmptyChannel(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	channelID := gocql.MustRandomUUID()
	msgs, err := repo.ListMessages(ctx, channelID, nil, nil, 50)
	require.NoError(t, err)
	assert.Empty(t, msgs)
}

func TestCreateMessage_WithReplyTo(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	channelID := gocql.MustRandomUUID()
	authorID := gocql.MustRandomUUID()

	original := makeMessage(channelID, authorID, "original message")
	require.NoError(t, repo.CreateMessage(ctx, original))

	reply := makeMessage(channelID, authorID, "this is a reply")
	reply.ReplyToId = original.Id
	require.NoError(t, repo.CreateMessage(ctx, reply))

	got, err := repo.GetMessage(ctx, channelID, reply.Id)
	require.NoError(t, err)
	assert.Equal(t, original.Id, uuidValue(got.ReplyToId))
}

func TestGetReactions_NoReactions(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	channelID := gocql.MustRandomUUID()
	authorID := gocql.MustRandomUUID()
	msg := makeMessage(channelID, authorID, "no reactions here")
	require.NoError(t, repo.CreateMessage(ctx, msg))

	reactions, err := repo.GetReactions(ctx, channelID, msg.Id, gocql.MustRandomUUID())
	require.NoError(t, err)
	assert.Empty(t, reactions)
}

func TestAddReaction_Idempotent(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	channelID := gocql.MustRandomUUID()
	authorID := gocql.MustRandomUUID()
	msg := makeMessage(channelID, authorID, "idempotent react")
	require.NoError(t, repo.CreateMessage(ctx, msg))

	user := gocql.MustRandomUUID()

	// Add the same reaction twice.
	require.NoError(t, repo.AddReaction(ctx, channelID, msg.Id, "star", user))
	require.NoError(t, repo.AddReaction(ctx, channelID, msg.Id, "star", user))

	reactions, err := repo.GetReactions(ctx, channelID, msg.Id, user)
	require.NoError(t, err)
	require.Len(t, reactions, 1)
	assert.Equal(t, int32(1), reactions[0].Count, "duplicate INSERT should be idempotent in Scylla")
}
