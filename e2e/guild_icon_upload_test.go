package e2e

import (
	"bytes"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	authv1 "github.com/ananddub/ndiscord_backend/gen/auth/v1"
	guildv1 "github.com/ananddub/ndiscord_backend/gen/guild/v1"
)

func TestGuildIconUpload(t *testing.T) {
	conn := newConn(t)
	authClient := authv1.NewAuthServiceClient(conn)
	guildClient := guildv1.NewGuildServiceClient(conn)

	ts := time.Now().UnixNano()
	owner := registerAndVerify(t, authClient,
		fmt.Sprintf("iconowner%d", ts),
		fmt.Sprintf("iconowner%d@test.local", ts),
		"testpass123")
	ctx := owner.ctx()

	createResp, err := guildClient.CreateGuild(ctx, &guildv1.CreateGuildRequest{
		Name:        "Icon Test Guild",
		Description: "e2e icon upload test",
	})
	require.NoError(t, err)
	require.Empty(t, createResp.Guild.IconUrl, "guild should start with empty icon_url")
	guildID := createResp.Guild.Id

	pngBytes := tinyPNG()

	upResp, err := guildClient.UploadGuildIcon(ctx, &guildv1.UploadGuildIconRequest{
		GuildId:     guildID,
		Filename:    "icon.png",
		ContentType: "image/png",
		Data:        pngBytes,
	})
	require.NoError(t, err, "UploadGuildIcon failed")
	require.NotEmpty(t, upResp.IconUrl, "response must include icon_url")
	assert.Equal(t, upResp.IconUrl, upResp.Guild.IconUrl, "response guild should have the new icon_url")

	// Verify the icon was actually stored: GET the presigned URL.
	// Server now signs with MINIO_PUBLIC_ENDPOINT (localhost:9000), so the URL
	// is directly reachable from the host.
	require.True(t, strings.Contains(upResp.IconUrl, "localhost:9000"),
		"icon_url should use public endpoint, got: %s", upResp.IconUrl)
	httpClient := &http.Client{Timeout: 5 * time.Second}
	httpResp, err := httpClient.Get(upResp.IconUrl)
	require.NoError(t, err)
	defer httpResp.Body.Close()
	require.Equal(t, http.StatusOK, httpResp.StatusCode, "presigned URL should return 200")

	downloaded, err := io.ReadAll(httpResp.Body)
	require.NoError(t, err)
	assert.Equal(t, pngBytes, downloaded, "downloaded bytes must match uploaded")

	// Verify GetGuild returns the updated icon_url.
	getResp, err := guildClient.GetGuild(ctx, &guildv1.GetGuildRequest{GuildId: guildID})
	require.NoError(t, err)
	assert.Equal(t, upResp.IconUrl, getResp.Guild.IconUrl, "GetGuild should return persisted icon_url")

	// Negative: non-owner cannot upload.
	other := registerAndVerify(t, authClient,
		fmt.Sprintf("iconother%d", ts),
		fmt.Sprintf("iconother%d@test.local", ts),
		"testpass123")
	_, err = guildClient.UploadGuildIcon(other.ctx(), &guildv1.UploadGuildIconRequest{
		GuildId:     guildID,
		Filename:    "icon.png",
		ContentType: "image/png",
		Data:        pngBytes,
	})
	require.Error(t, err, "non-owner should not be able to upload guild icon")

	// Negative: unsupported content type rejected by protovalidate.
	_, err = guildClient.UploadGuildIcon(ctx, &guildv1.UploadGuildIconRequest{
		GuildId:     guildID,
		Filename:    "icon.txt",
		ContentType: "text/plain",
		Data:        []byte("not-an-image"),
	})
	require.Error(t, err, "text/plain should be rejected")
}

// tinyPNG returns a minimal valid 1x1 red PNG.
func tinyPNG() []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	writeChunk := func(typ string, data []byte) {
		lenBuf := []byte{
			byte(len(data) >> 24),
			byte(len(data) >> 16),
			byte(len(data) >> 8),
			byte(len(data)),
		}
		buf.Write(lenBuf)
		buf.WriteString(typ)
		buf.Write(data)
		crc := crc32.NewIEEE()
		crc.Write([]byte(typ))
		crc.Write(data)
		sum := crc.Sum32()
		buf.Write([]byte{byte(sum >> 24), byte(sum >> 16), byte(sum >> 8), byte(sum)})
	}
	writeChunk("IHDR", []byte{0, 0, 0, 1, 0, 0, 0, 1, 8, 2, 0, 0, 0})
	// Pre-computed zlib-compressed raw scanline for 1x1 red pixel
	writeChunk("IDAT", []byte{0x78, 0x9c, 0x62, 0xf8, 0xcf, 0xc0, 0x00, 0x00, 0x00, 0x04, 0x00, 0x01, 0x5c, 0xcd, 0xff, 0x69})
	writeChunk("IEND", nil)
	return buf.Bytes()
}
