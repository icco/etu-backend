# Native mobile login

`etu.AuthService/Login` is a public unary native gRPC RPC using the existing
`AuthenticateRequest` (email/password) and `CreateApiKeyResponse` messages.

It validates credentials through the same disabled-account and lockout checks as
`Authenticate`, then issues a normal revocable API key named `etu-mobile`, scoped
to the verified user. Caller-provided authorization metadata cannot change the
owner. Failed authentication never creates a key. The raw key is returned once;
only its bcrypt hash is stored.

Clients send the returned `raw_key` as the raw `authorization` metadata value
(without `Bearer`). `VerifyApiKey` supplies the user ID and `GetUser` retrieves
the user profile. Existing API-key revocation via Settings applies to sessions.

Deploy this backend before the native mobile login update. No database migration
or gateway is needed. Existing `Authenticate` callers retain their behavior and
do not create keys on every web login. The TypeScript proto package gains the
new method on the next normal publish.
