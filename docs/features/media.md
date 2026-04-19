# Media

File upload orchestration. Clients get a presigned PUT URL, upload
directly to MinIO, then confirm. The message feature resolves
confirmed media ids into `ChatAttachment` entries at send time.

## Package

[internal/features/media](../../internal/features/media)

## gRPC surface (proto: [media/v1/media.proto](../../internal/features/media/proto/media/v1/media.proto))

| RPC | What it does |
|---|---|
| `RequestUpload` | Return presigned PUT URL + media_id; creates `media_files` row (unconfirmed) |
| `ConfirmUpload` | Flip `confirmed = true` after client upload succeeds |
| `GetUploadURL` | Re-issue a presigned GET for an owned object |
| `DeleteMedia` | Owner-only soft delete |

## Data it owns

- `media_files` row (Postgres)
- MinIO `uploads/<userID>/<UUID>_<filename>` object

## Flow

```
client.RequestUpload(filename, content_type, size)
  │
  ├─ validate size, content-type (if restricted)
  ├─ generate UUID v4 → objectKey = uploads/<userID>/<UUID>_<filename>
  ├─ INSERT media_files { id, uploader_id, filename, content_type, size, bucket_key, confirmed=false }
  ├─ MinIO PresignedPutObject(objectKey, ttl=10min) → PUT URL
  └─ return { media_id, put_url, expires_at }
      ↓
client PUT <put_url> with binary body        (no server involvement)
      ↓
client.ConfirmUpload(media_id)
  ├─ MinIO StatObject(objectKey)            (verify it actually exists)
  ├─ UPDATE media_files SET confirmed = true
  └─ return { get_url (presigned, 7d) }
```

Two-phase commit means a crashed client (never hits ConfirmUpload)
leaves an unconfirmed row + object. Periodic sweeper (TODO) cleans
both after 24h.

## Attachment resolution for messages

`message.Service.SendMessage` takes a list of `attachmentIDs`:

```go
if s.media != nil {
    for _, fid := range attachmentIDs {
        filename, url, contentType, size, err := s.media.ResolveAttachment(ctx, fid, userID)
        if err != nil { continue }  // skip, don't fail the whole send
        s.repo.AddMessageAttachment(ctx, chUUID, msg.ID, attID, filename, url, contentType, size)
    }
}
```

`ResolveAttachment(fid, uploaderID)` is the `MediaResolver` interface —
implemented here. It:

1. Reads the `media_files` row.
2. Rejects if `confirmed = false` (the caller never finished upload).
3. Rejects if `uploader_id ≠ caller` (can't attach someone else's
   upload).
4. Signs a fresh GET URL.
5. Returns filename, URL, content-type, size.

## MinIO clients

Two `*minio.Client` instances are injected:

- **Internal** (`MINIO_ENDPOINT`) — used for puts and `StatObject`
  inside the service VPC.
- **Signer** (`MINIO_PUBLIC_ENDPOINT`) — used for presigned URLs that
  clients actually hit. Can point at a CDN.

## Validation

- Content-type is NOT enforced by `RequestUpload` by default — the
  caller is the source of truth. The message service does a
  higher-level check for specific features (avatars restrict to
  image/*, for example).
- Size is enforced up front so a malicious client can't pre-sign a
  100 GB PUT URL.
- Deletes are owner-only; guild admins can't delete other users'
  attachments (Discord parity).

## Failure modes

- **Client never confirms** → unconfirmed row + object; sweeper
  deletes both after 24h.
- **Client confirms but object doesn't exist** → `StatObject` fails,
  `ConfirmUpload` returns `NotFound`; row stays unconfirmed, will be
  swept.
- **Media id referenced after deletion** → `ResolveAttachment`
  returns error, attachment skipped. Pre-send checks are left to the
  client UI (disable the "send" button while the attachment is
  still uploading).
