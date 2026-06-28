# Auth

Handles signup, email verification, login, JWT issuance, refresh-token
rotation and logout. The only feature every other service depends on
transitively — the auth interceptor populates `UserIDKey` in context,
and everything else reads it.

## Package

[internal/features/auth](../../internal/features/auth)

## gRPC surface (proto: [auth/v1/auth.proto](../../internal/features/auth/proto/auth/v1/auth.proto))

| RPC | Auth? | What it does |
|---|---|---|
| `Register` | no | Create unverified user, send email OTP |
| `VerifyOTP` | no | Confirm OTP, flip `verified=true`, issue tokens |
| `ResendOTP` | no | Regenerate and email a new OTP (rate-limited) |
| `Login` | no | Password check → issue access + refresh tokens |
| `RefreshToken` | no (token is auth) | Rotate access token from a valid refresh token |
| `Logout` | yes | Invalidate refresh token, mark user OFFLINE via presence |
| `ValidateToken` | no | Internal helper — verify a token and return user id |

## Data it owns

- `users` row (shared with `user` feature)
- `otp:<email>` Redis key (5 min TTL)
- `refresh:<token>` Redis key (7 day TTL, configurable)

## Flow: signup → verified session

```
client.Register(email, username, password)
   │
   ├─ bcrypt(password) → users row with verified=false
   ├─ random 6-digit OTP → SET otp:<email> EX 300
   └─ SMTP sends OTP email
   ↓
client.VerifyOTP(email, otp)
   │
   ├─ GET otp:<email>  — compare constant-time, DEL on match
   ├─ UPDATE users SET verified = true
   ├─ mint JWT access token (HS256, 7d)
   ├─ mint JWT refresh token, SET refresh:<token> EX 7d → userID
   └─ return { access_token, refresh_token, user }
```

## Flow: access token refresh

```
client.RefreshToken(refresh_token)
   │
   ├─ verify signature + claims
   ├─ GET refresh:<token>   — must exist (prevents replay after logout)
   ├─ DEL refresh:<token>   — rotate
   ├─ mint new access + refresh
   └─ return { access_token, refresh_token }
```

Refresh tokens are single-use: every refresh rotates the token. A
stolen token used twice fails the second time.

## Flow: logout

```
client.Logout()
   │
   ├─ pull userID from ctx
   ├─ DEL refresh:<token>
   └─ presence.SetOffline(userID)   — broadcasts FRIEND_EVENT_PRESENCE_UPDATE
```

The presence side effect is injected through
`authHandler.SetPresenceUpdater(presenceSvc)` in
[app/services.go](../../internal/app/services.go).

## Middleware — `AuthInterceptor`

Lives in [internal/shared/middleware/auth.go](../../internal/shared/middleware/auth.go).
Every unary / stream call (except a hard-coded allowlist of public
methods) runs through it:

1. Pull `authorization` from gRPC metadata.
2. Strip `Bearer ` prefix.
3. `jwt.ParseWithClaims` using `JWT_SECRET` (HS256).
4. On success: `ctx.WithValue(UserIDKey, claims.Subject)`.
5. Handlers pull it out with `middleware.UserIDFromContext(ctx)`.

Public methods (explicitly bypassed):

- `/auth.v1.AuthService/Register`
- `/auth.v1.AuthService/VerifyOTP`
- `/auth.v1.AuthService/ResendOTP`
- `/auth.v1.AuthService/Login`
- `/auth.v1.AuthService/RefreshToken`
- `/auth.v1.AuthService/ValidateToken`
- `/guild.v1.GuildService/PreviewInvite` — lets a signed-out user
  preview an invite landing page.

## Why HMAC JWT (not RSA / PASETO)

- Single backend, one secret — asymmetric keys are overkill.
- Access tokens are short-lived; refresh-token revocation lives in
  Redis; signature rotation is a config change.
- If we ever need multi-tenant signing (federated identity, partner
  API), swap to RS256 — the `jwt.Parser` call is the only change.

## Failure modes

- **SMTP down** → `Register` returns; OTP row stays in Redis for 5
  min; `ResendOTP` will attempt again. We do NOT roll back the user
  row on email failure; verification is async.
- **Redis down on login** → refresh token can't be stored; return
  `Internal`. Access token is still issued but the client has no way
  to refresh it — caller should surface the error.
- **JWT secret rotated** → every outstanding token invalidates. Users
  re-login. Document when rotating.
