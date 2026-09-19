# Native mobile login

`etu.AuthService/Login` is a public unary native gRPC RPC using the existing
`AuthenticateRequest` (email/password) and `CreateApiKeyResponse` messages.

It validates credentials through the same disabled-account and lockout checks as
`Authenticate`, then issues a normal revocable API key named `etu-mobile`, scoped
to the verified user. Caller-provided authorization metadata cannot change the
owner. Failed authentication never creates a key. The raw key is returned once;
only its bcrypt hash is stored.

`Login` applies admission controls before database access or bcrypt work:

- Per server instance: 1 request/second, burst 10, across all callers.
- Per normalized account: 5 requests/minute, burst 5. A fixed set of 4096 hashed
  buckets bounds limiter memory; collisions share a quota rather than bypass it.
- At most 4 concurrent Login operations per server instance, rejected rather
  than queued when busy. Successful and failed requests both consume quota.

Each user can hold at most 10 `etu-mobile` keys created by Login. A transaction
locks the user's row before counting existing keys and inserting, so concurrent
requests and different server instances cannot race past the cap. The cap check
runs before generating/hashing another key. No existing keys are evicted; revoke
an old `etu-mobile` key in Settings before logging in again at the limit.
Both throttling and the session cap return `ResourceExhausted`. Admission quotas
reset on process restart, but the database-backed session cap survives restarts.

Clients send the returned `raw_key` as the raw `authorization` metadata value
(without `Bearer`). `VerifyApiKey` supplies the user ID and `GetUser` retrieves
the user profile. Existing API-key revocation via Settings applies to sessions.

Deploy this backend before the native mobile login update. No database migration
or gateway is needed. Existing `Authenticate` callers retain their behavior and
do not create keys on every web login. The TypeScript proto package gains the
new method on the next normal publish.
