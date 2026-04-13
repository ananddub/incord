package message

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/gocql/gocql"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	messagev1 "github.com/ananddub/ndiscord_backend/gen/message/v1"
	"github.com/ananddub/ndiscord_backend/internal/shared/logger"
	"github.com/ananddub/ndiscord_backend/internal/shared/middleware"
)

const testSecret = "test-secret"

func init() {
	logger.Init("debug")
}

// ---------------------------------------------------------------------------
// Auth helpers
// ---------------------------------------------------------------------------

func makeToken(userID string) string {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID,
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	s, _ := token.SignedString([]byte(testSecret))
	return s
}

func authCtx(userID string) context.Context {
	md := metadata.New(map[string]string{"authorization": "Bearer " + makeToken(userID)})
	return metadata.NewOutgoingContext(context.Background(), md)
}

// ---------------------------------------------------------------------------
// Infrastructure setup
// ---------------------------------------------------------------------------

func setupRedis(t *testing.T) *redis.Client {
	t.Helper()
	ctx := context.Background()

	redisReq := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections").WithStartupTimeout(30 * time.Second),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: redisReq,
		Started:          true,
	})
	require.NoError(t, err, "failed to start redis")

	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "6379")
	require.NoError(t, err)

	rdb := redis.NewClient(&redis.Options{Addr: fmt.Sprintf("%s:%s", host, port.Port())})
	require.NoError(t, rdb.Ping(ctx).Err(), "failed to ping redis")

	t.Cleanup(func() {
		rdb.Close()
		_ = container.Terminate(ctx)
	})
	return rdb
}

// setupMessageServer spins up ScyllaDB and Redis, builds the full
// handler stack, starts a gRPC server with auth interceptor, and returns
// a connected client.
func setupMessageServer(t *testing.T) messagev1.MessageServiceClient {
	t.Helper()

	session := setupScylla(t)
	rdb := setupRedis(t)

	repo := NewRepository(session)
	svc := NewService(repo, rdb, nil)
	handler := NewHandler(svc)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := grpc.NewServer(
		grpc.UnaryInterceptor(middleware.AuthInterceptor(testSecret)),
	)
	messagev1.RegisterMessageServiceServer(srv, handler)

	go srv.Serve(lis)
	t.Cleanup(func() { srv.Stop() })

	conn, err := grpc.NewClient(
		lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	return messagev1.NewMessageServiceClient(conn)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestHandler_SendAndGetMessage(t *testing.T) {
	client := setupMessageServer(t)

	userID := gocql.MustRandomUUID().String()
	channelID := gocql.MustRandomUUID().String()
	ctx := authCtx(userID)

	// Send a message.
	sendResp, err := client.SendMessage(ctx, &messagev1.SendMessageRequest{
		ChannelId: channelID,
		Content:   "hello from handler test",
		Type:      messagev1.MessageType_MESSAGE_TYPE_DEFAULT,
	})
	require.NoError(t, err)
	require.NotNil(t, sendResp.Message)
	assert.Equal(t, channelID, sendResp.Message.ChannelId)
	assert.Equal(t, userID, sendResp.Message.AuthorId)
	assert.Equal(t, "hello from handler test", sendResp.Message.Content)
	msgID := sendResp.Message.Id

	// Get the message back.
	getResp, err := client.GetMessage(ctx, &messagev1.GetMessageRequest{
		ChannelId: channelID,
		MessageId: msgID,
	})
	require.NoError(t, err)
	require.NotNil(t, getResp.Message)
	assert.Equal(t, msgID, getResp.Message.Id)
	assert.Equal(t, "hello from handler test", getResp.Message.Content)
}

func TestHandler_ListMessages(t *testing.T) {
	client := setupMessageServer(t)

	userID := gocql.MustRandomUUID().String()
	channelID := gocql.MustRandomUUID().String()
	ctx := authCtx(userID)

	// Send 3 messages.
	for i := 0; i < 3; i++ {
		_, err := client.SendMessage(ctx, &messagev1.SendMessageRequest{
			ChannelId: channelID,
			Content:   fmt.Sprintf("msg-%d", i),
			Type:      messagev1.MessageType_MESSAGE_TYPE_DEFAULT,
		})
		require.NoError(t, err)
		time.Sleep(10 * time.Millisecond) // distinct TimeUUIDs
	}

	listResp, err := client.ListMessages(ctx, &messagev1.ListMessagesRequest{
		ChannelId: channelID,
		Limit:     10,
	})
	require.NoError(t, err)
	assert.Len(t, listResp.Messages, 3)
}

func TestHandler_EditMessage(t *testing.T) {
	client := setupMessageServer(t)

	userID := gocql.MustRandomUUID().String()
	channelID := gocql.MustRandomUUID().String()
	ctx := authCtx(userID)

	sendResp, err := client.SendMessage(ctx, &messagev1.SendMessageRequest{
		ChannelId: channelID,
		Content:   "original",
		Type:      messagev1.MessageType_MESSAGE_TYPE_DEFAULT,
	})
	require.NoError(t, err)
	msgID := sendResp.Message.Id

	editResp, err := client.EditMessage(ctx, &messagev1.EditMessageRequest{
		ChannelId: channelID,
		MessageId: msgID,
		Content:   "updated content",
	})
	require.NoError(t, err)
	assert.Equal(t, "updated content", editResp.Message.Content)
	assert.NotNil(t, editResp.Message.EditedAt, "edited_at should be set")
}

func TestHandler_EditMessage_NotAuthor(t *testing.T) {
	client := setupMessageServer(t)

	authorID := gocql.MustRandomUUID().String()
	otherID := gocql.MustRandomUUID().String()
	channelID := gocql.MustRandomUUID().String()

	sendResp, err := client.SendMessage(authCtx(authorID), &messagev1.SendMessageRequest{
		ChannelId: channelID,
		Content:   "author's message",
		Type:      messagev1.MessageType_MESSAGE_TYPE_DEFAULT,
	})
	require.NoError(t, err)

	_, err = client.EditMessage(authCtx(otherID), &messagev1.EditMessageRequest{
		ChannelId: channelID,
		MessageId: sendResp.Message.Id,
		Content:   "hacked",
	})
	require.Error(t, err)
	assert.Equal(t, codes.PermissionDenied, status.Code(err))
}

func TestHandler_DeleteMessage(t *testing.T) {
	client := setupMessageServer(t)

	userID := gocql.MustRandomUUID().String()
	channelID := gocql.MustRandomUUID().String()
	ctx := authCtx(userID)

	sendResp, err := client.SendMessage(ctx, &messagev1.SendMessageRequest{
		ChannelId: channelID,
		Content:   "to be deleted",
		Type:      messagev1.MessageType_MESSAGE_TYPE_DEFAULT,
	})
	require.NoError(t, err)
	msgID := sendResp.Message.Id

	_, err = client.DeleteMessage(ctx, &messagev1.DeleteMessageRequest{
		ChannelId: channelID,
		MessageId: msgID,
	})
	require.NoError(t, err)

	// Verify it's gone.
	_, err = client.GetMessage(ctx, &messagev1.GetMessageRequest{
		ChannelId: channelID,
		MessageId: msgID,
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestHandler_PinAndUnpinMessage(t *testing.T) {
	client := setupMessageServer(t)

	userID := gocql.MustRandomUUID().String()
	channelID := gocql.MustRandomUUID().String()
	ctx := authCtx(userID)

	sendResp, err := client.SendMessage(ctx, &messagev1.SendMessageRequest{
		ChannelId: channelID,
		Content:   "pin me",
		Type:      messagev1.MessageType_MESSAGE_TYPE_DEFAULT,
	})
	require.NoError(t, err)
	msgID := sendResp.Message.Id

	// Pin.
	_, err = client.PinMessage(ctx, &messagev1.PinMessageRequest{
		ChannelId: channelID,
		MessageId: msgID,
	})
	require.NoError(t, err)

	getResp, err := client.GetMessage(ctx, &messagev1.GetMessageRequest{
		ChannelId: channelID,
		MessageId: msgID,
	})
	require.NoError(t, err)
	assert.True(t, getResp.Message.Pinned)

	// Unpin.
	_, err = client.UnpinMessage(ctx, &messagev1.UnpinMessageRequest{
		ChannelId: channelID,
		MessageId: msgID,
	})
	require.NoError(t, err)

	getResp, err = client.GetMessage(ctx, &messagev1.GetMessageRequest{
		ChannelId: channelID,
		MessageId: msgID,
	})
	require.NoError(t, err)
	assert.False(t, getResp.Message.Pinned)
}

func TestHandler_AddAndRemoveReaction(t *testing.T) {
	client := setupMessageServer(t)

	userID := gocql.MustRandomUUID().String()
	channelID := gocql.MustRandomUUID().String()
	ctx := authCtx(userID)

	sendResp, err := client.SendMessage(ctx, &messagev1.SendMessageRequest{
		ChannelId: channelID,
		Content:   "react to this",
		Type:      messagev1.MessageType_MESSAGE_TYPE_DEFAULT,
	})
	require.NoError(t, err)
	msgID := sendResp.Message.Id

	// Add reaction.
	_, err = client.AddReaction(ctx, &messagev1.AddReactionRequest{
		ChannelId: channelID,
		MessageId: msgID,
		Emoji:     "thumbsup",
	})
	require.NoError(t, err)

	// Verify reaction appears in GetMessage.
	getResp, err := client.GetMessage(ctx, &messagev1.GetMessageRequest{
		ChannelId: channelID,
		MessageId: msgID,
	})
	require.NoError(t, err)
	require.Len(t, getResp.Message.Reactions, 1)
	assert.Equal(t, "thumbsup", getResp.Message.Reactions[0].Emoji)
	assert.Equal(t, int32(1), getResp.Message.Reactions[0].Count)
	assert.True(t, getResp.Message.Reactions[0].Me)

	// Remove reaction.
	_, err = client.RemoveReaction(ctx, &messagev1.RemoveReactionRequest{
		ChannelId: channelID,
		MessageId: msgID,
		Emoji:     "thumbsup",
	})
	require.NoError(t, err)

	getResp, err = client.GetMessage(ctx, &messagev1.GetMessageRequest{
		ChannelId: channelID,
		MessageId: msgID,
	})
	require.NoError(t, err)
	assert.Empty(t, getResp.Message.Reactions)
}

func TestHandler_AckMessage(t *testing.T) {
	client := setupMessageServer(t)

	userID := gocql.MustRandomUUID().String()
	channelID := gocql.MustRandomUUID().String()
	ctx := authCtx(userID)

	sendResp, err := client.SendMessage(ctx, &messagev1.SendMessageRequest{
		ChannelId: channelID,
		Content:   "ack me",
		Type:      messagev1.MessageType_MESSAGE_TYPE_DEFAULT,
	})
	require.NoError(t, err)

	_, err = client.AckMessage(ctx, &messagev1.AckMessageRequest{
		ChannelId: channelID,
		MessageId: sendResp.Message.Id,
	})
	require.NoError(t, err)
}

func TestHandler_StartTyping(t *testing.T) {
	client := setupMessageServer(t)

	userID := gocql.MustRandomUUID().String()
	channelID := gocql.MustRandomUUID().String()
	ctx := authCtx(userID)

	_, err := client.StartTyping(ctx, &messagev1.StartTypingRequest{
		ChannelId: channelID,
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Validation / error tests
// ---------------------------------------------------------------------------

func TestHandler_SendMessage_MissingChannelID(t *testing.T) {
	client := setupMessageServer(t)
	ctx := authCtx(gocql.MustRandomUUID().String())

	_, err := client.SendMessage(ctx, &messagev1.SendMessageRequest{
		Content: "no channel",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestHandler_SendMessage_MissingContent(t *testing.T) {
	client := setupMessageServer(t)
	ctx := authCtx(gocql.MustRandomUUID().String())

	_, err := client.SendMessage(ctx, &messagev1.SendMessageRequest{
		ChannelId: gocql.MustRandomUUID().String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestHandler_SendMessage_Unauthenticated(t *testing.T) {
	client := setupMessageServer(t)

	_, err := client.SendMessage(context.Background(), &messagev1.SendMessageRequest{
		ChannelId: gocql.MustRandomUUID().String(),
		Content:   "no auth",
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unauthenticated, status.Code(err))
}

func TestHandler_GetMessage_NotFound(t *testing.T) {
	client := setupMessageServer(t)
	ctx := authCtx(gocql.MustRandomUUID().String())

	_, err := client.GetMessage(ctx, &messagev1.GetMessageRequest{
		ChannelId: gocql.MustRandomUUID().String(),
		MessageId: gocql.TimeUUID().String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}
