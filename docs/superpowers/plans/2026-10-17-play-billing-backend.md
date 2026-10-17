# Google Play Billing (backend and infra) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** etu-backend verifies Google Play subscriptions, tracks them through RTDN, and shares one paid status with Stripe without either store overwriting the other.

**Architecture:** A new `BillingService.VerifyPlayPurchase` RPC and a `POST /billing/play/rtdn` HTTP handler both call one `PlaySync` routine. It fetches `purchases.subscriptionsv2`, maps the state, and writes `PlaySubscription` plus the user's subscription columns under the provider ownership rule. Stripe writes go through the same rule.

**Tech Stack:** Go, gRPC, GORM + Atlas migrations, `google.golang.org/api/androidpublisher/v3`, `google.golang.org/api/idtoken`, OpenTofu (icco.me).

**Spec:** `docs/superpowers/specs/2026-10-17-play-billing-design.md`

## Global Constraints

- Load the `go-repo` skill before writing Go. Follow AGENTS.md: table-driven tests with go-sqlmock and go-cmp, and run `task test` and `task lint` for the touched packages.
- Package name `com.timeclimbers.etu`, product ID `etu`, base plans `yearly` and `monthly`.
- `billingProvider` values: `stripe`, `play`, or NULL.
- Status mapping: ACTIVE in a free-trial phase -> `trialing`; ACTIVE -> `active`; CANCELED -> `active` + `cancelAtPeriodEnd=true`; IN_GRACE_PERIOD -> `past_due`; ON_HOLD, PAUSED -> `unpaid`; EXPIRED, PENDING_PURCHASE_CANCELED, revoked or voided -> `canceled`; PENDING -> no change.
- Stripe write rejected by ownership returns gRPC `FailedPrecondition` with the message `billing managed by google play`.
- Env: `PLAY_PACKAGE_NAME`, `PLAY_RTDN_AUDIENCE`, `PLAY_RTDN_SERVICE_ACCOUNT`, `PLAY_ALLOW_TEST_PURCHASES`. Missing Play config: the RPC returns `FailedPrecondition`, RTDN returns 503, and the server still starts.
- RTDN URL and audience: `https://be.etu.timeclimbers.com/billing/play/rtdn`.
- No code comments unless a line's reason is not obvious. No dependency overrides. Never force-push. Draft PR icco/etu-backend#177 is the backend PR.

## Review Focus

- The same purchase token is verified twice (app retry plus RTDN racing): the result must be identical and acknowledge must not be called twice. Covered in Task 4.
- An RTDN arrives for a token the backend has never seen, before the app called verify: link via obfuscated account ID. If that user doesn't exist, return 2xx and log. Covered in Task 5.
- An upgrade or re-signup sets `linkedPurchaseToken`: the old row is marked superseded and must not later downgrade the user. Covered in Task 2.
- Cancelling the other store fails during a takeover: the user keeps the old state, the purchase stays unacknowledged, and the RPC returns `Unavailable`. Covered in Task 4.
- Pub/Sub OIDC token with the wrong audience or wrong email: 401, nothing written. Covered in Task 5.

---

### Task 1: Schema and proto

**Files:**
- Modify: `internal/models/models.go` (User gets `BillingProvider *string` with gorm column `billingProvider`; new `PlaySubscription` model with table name `PlaySubscription`)
- Modify: `internal/db/db.go` (AutoMigrate list), `internal/db/columns.go` (`colBillingProvider`)
- Create: `migrations/<timestamp>_play_billing.sql` via `task migrate-diff NAME=play_billing`, then append `UPDATE "User" SET "billingProvider" = 'stripe' WHERE "stripeSubscriptionId" IS NOT NULL;` and rehash `atlas.sum` (`atlas migrate hash`)
- Modify: `proto/etu.proto`, regenerated `proto/*.pb.go`, `packages/etu-proto` build

**Interfaces:**
- Produces: `models.PlaySubscription{PurchaseToken string (PK, column purchaseToken); UserID string (userId, FK User cascade, index); ProductID, BasePlanID, State string; ExpiryTime *time.Time; AutoRenewing, Acknowledged, Superseded bool; LinkedPurchaseToken *string; CreatedAt, UpdatedAt time.Time}`
- Produces proto: `User.billing_provider` (optional string, next free field number). `service BillingService { rpc VerifyPlayPurchase(VerifyPlayPurchaseRequest) returns (VerifyPlayPurchaseResponse); }` with `VerifyPlayPurchaseRequest{string purchase_token = 1; string product_id = 2;}` and `VerifyPlayPurchaseResponse{User user = 1;}`. Document each field the way the existing proto does.
- `userToProto` sets `BillingProvider`.

- [ ] Write `TestUserToProtoBillingProvider` in `internal/service` asserting `BillingProvider` is copied when set and nil when unset.
- [ ] Run `go test ./internal/service -run TestUserToProtoBillingProvider`. Expect a compile failure.
- [ ] Add the models, proto, and generated code (`task proto`). Generate the migration as above.
- [ ] Run the test, `go build ./...`, and `atlas migrate lint` if available locally (otherwise CI's migrations workflow). Expect PASS.
- [ ] Commit `feat: add play subscription schema and billing proto`, and push.

### Task 2: Ownership rule and Play persistence in `internal/db`

**Files:**
- Create: `internal/db/billing_provider.go`, `internal/db/billing_provider_test.go`
- Modify: `internal/db/db.go` (`updateUserSubscription`), `internal/service/auth.go:237-243`

**Interfaces:**
- Produces: `var ErrBillingProviderMismatch = errors.New("billing managed by google play")`
- Produces: `const BillingProviderStripe = "stripe"`, `BillingProviderPlay = "play"`
- Produces: `func ownershipClause(provider string, grace time.Duration) (string, []any)` returning SQL for: `"billingProvider" IS NULL OR "billingProvider" = ? OR NOT (<paid>)`, where `<paid>` means status in `AIEligibleSubscriptionStatuses` excluding `past_due`, or (`past_due` and `"pastDueSince" > now() - grace`).
- Produces: `type PlaySubscriptionUpdate struct { PurchaseToken, UserID, ProductID, BasePlanID, State string; ExpiryTime *time.Time; AutoRenewing, Acknowledged bool; LinkedPurchaseToken *string; Status string /* empty = leave user status */; CancelAtPeriodEnd bool }`
- Produces: `func (db *DB) ApplyPlaySubscription(ctx context.Context, in PlaySubscriptionUpdate, grace time.Duration) (*User, error)`. In one transaction: upsert the row; mark `LinkedPurchaseToken`'s row `superseded`; if the row being written is itself superseded, or `Status` is empty, skip the user write; otherwise update the user's status, `subscriptionEnd`, `cancelAtPeriodEnd`, `pastDueSince` (same COALESCE rule as Stripe), and `billingProvider='play'`, guarded by `ownershipClause("play")`. If 0 rows are affected because of ownership, return `ErrBillingProviderMismatch` (the error type is shared; the caller decides what to do).
- Produces: `func (db *DB) PlaySubscriptionUser(ctx context.Context, purchaseToken string) (string, error)` (userId or "" if unknown), and `func (db *DB) ActivePlaySubscriptions(ctx context.Context, userID string) ([]PlaySubscription, error)` (not superseded, state not EXPIRED or PENDING_PURCHASE_CANCELED).
- Changes: `updateUserSubscription` adds `ownershipClause("stripe")` and sets `billingProvider='stripe'`. When 0 rows are affected and the user exists with the Play provider, it returns `ErrBillingProviderMismatch`. `AuthService.UpdateUserSubscription` maps that error to `codes.FailedPrecondition` with the message `billing managed by google play`.
- Grace: thread `PaywallGrace` into `db.DB` (a field set at construction from `auth.NewPaywallFromEnv`) rather than changing existing signatures.

- [ ] Write table tests with sqlmock:
  - `TestApplyPlaySubscriptionClaimsUnownedUser`: the UPDATE matches the ownership WHERE clause and sets `billingProvider`.
  - `TestApplyPlaySubscriptionRejectedWhenStripeOwnsPaidUser`: 0 rows affected -> `ErrBillingProviderMismatch`.
  - `TestApplyPlaySubscriptionSupersedesLinkedToken`.
  - `TestApplyPlaySubscriptionSupersededRowSkipsUser`.
  - `TestUpdateUserSubscriptionRejectedWhenPlayOwns`.
  - `TestUpdateUserSubscriptionTakesOverLapsedPlayUser`.
- [ ] Run `go test ./internal/db -run 'PlaySubscription|UpdateUserSubscription'`. Expect FAIL.
- [ ] Implement.
- [ ] Run `go test ./internal/db ./internal/service` and expect PASS. Existing Stripe tests must still pass; update their expected SQL for the new clause without weakening their assertions.
- [ ] Commit `feat: enforce one billing provider per user`, and push.

### Task 3: Play API client and state mapping

**Files:**
- Create: `internal/billing/play.go`, `internal/billing/play_test.go`

**Interfaces:**
- Produces: `type PlayPurchase struct { State string; ProductID, BasePlanID string; ExpiryTime *time.Time; AutoRenewing, Acknowledged, Test, FreeTrial bool; ObfuscatedAccountID string; LinkedPurchaseToken *string }`
- Produces: `type PlayAPI interface { GetSubscription(ctx context.Context, token string) (*PlayPurchase, error); Acknowledge(ctx context.Context, productID, token string) error; Cancel(ctx context.Context, token string) error }`
- Produces: `func NewPlay(ctx context.Context, packageName string) (*Play, error)` using ADC and `androidpublisher.NewService`. `GetSubscription` uses `Purchases.Subscriptionsv2.Get`, `Acknowledge` uses `Purchases.Subscriptions.Acknowledge`, and `Cancel` uses `Purchases.Subscriptionsv2.Cancel`. A 404 or 410 from the API returns `ErrPlayPurchaseNotFound`.
- Produces: `func MapPlayStatus(p *PlayPurchase) (status string, cancelAtPeriodEnd bool)`, implementing the Global Constraints mapping. PENDING returns `""`. `FreeTrial` comes from `lineItems[].offerPhase.freeTrial` being set.
- Produces: `var ErrPlayPurchaseNotFound = errors.New("play purchase not found")`

- [ ] Write `TestMapPlayStatus` with one table row per state in the constraint list, plus ACTIVE with FreeTrial. Write `TestPlayGetSubscriptionParses` using an `httptest.Server` and `option.WithEndpoint` and a canned SubscriptionPurchaseV2 JSON (Ada Lovelace's test account ID). Write `TestPlayGetSubscriptionNotFound`.
- [ ] Run `go test ./internal/billing -run Play`. Expect FAIL.
- [ ] Implement.
- [ ] Run the tests and expect PASS. Commit `feat: add google play subscriptions client`, and push.

### Task 4: `BillingService.VerifyPlayPurchase` and store switching

**Files:**
- Create: `internal/service/billing.go`, `internal/service/billing_test.go`, `internal/service/playsync.go`
- Modify: `internal/auth/paywall.go` (exempt `/etu.BillingService/VerifyPlayPurchase`), `internal/service/account.go` (DeleteAccount cancels Play)

**Interfaces:**
- Consumes: Task 2 db functions, Task 3 `PlayAPI` and `MapPlayStatus`, and `SubscriptionCanceler` (`internal/service/account.go:31`).
- Produces: `type PlaySync struct { DB *db.DB; Play billing.PlayAPI; Stripe SubscriptionCanceler; AllowTest bool; Grace time.Duration }` and `func (p *PlaySync) Sync(ctx context.Context, token, expectedUserID string) (*db.User, error)`. Steps:
  1. Fetch the purchase.
  2. Reject a test purchase unless `AllowTest`.
  3. Resolve the user: `expectedUserID`, else the stored row, else `ObfuscatedAccountID`. If `expectedUserID` is set and does not equal `ObfuscatedAccountID`, return `ErrPlayAccountMismatch`.
  4. Map the status and call `ApplyPlaySubscription`.
  5. On `ErrBillingProviderMismatch` with a paying Play status (one that grants access): cancel Stripe through `Stripe.CancelSubscriptions(customerID, subscriptionID)`. If the cancel fails, return `ErrTakeoverFailed` wrapping the error. Otherwise mark the user's Stripe status `canceled` with a direct update that bypasses ownership (`db.ReleaseStripeOwnership(ctx, userID)`, added in this task in `internal/db/billing_provider.go`), then retry `ApplyPlaySubscription` once.
  6. Acknowledge only if `!Acknowledged` and the user write succeeded. Then persist `Acknowledged=true`.
- Produces: `func NewBillingService(sync *PlaySync, packageName string) *BillingService`. `VerifyPlayPurchase` requires an API-key caller (not M2M), takes the user ID from context, checks `product_id == "etu"`, and maps errors:
  - nil sync: `FailedPrecondition "google play billing not configured"`
  - `ErrPlayPurchaseNotFound`: `NotFound`
  - `ErrPlayAccountMismatch`: `PermissionDenied`
  - `ErrTakeoverFailed`: `Unavailable`
  - test purchase rejected: `FailedPrecondition`
- Stripe-to-Play is the only takeover the backend runs here. Play-to-Stripe is handled in DeleteAccount and in Task 4b.
- Task 4b (same commit series): in `AuthService.UpdateUserSubscription`, when Stripe sends a paying status and the db returns `ErrBillingProviderMismatch` for a Play-owned user who is not paid, `updateUserSubscription` already allows it by the ownership rule. So the only extra work is to cancel that user's `ActivePlaySubscriptions` through `PlayAPI.Cancel` before the write when `billingProvider='play'`. If the cancel fails, return `Unavailable` so the webhook retries. `AuthService` gets an optional `play billing.PlayAPI` through a new setter `func (s *AuthService) SetPlay(p billing.PlayAPI)`, so the existing `NewAuthService` signature is unchanged.
- DeleteAccount: before deleting rows, call `PlayAPI.Cancel` for every `ActivePlaySubscriptions` row, and abort with `Unavailable` on failure. If Play is not configured and Play rows exist, return `FailedPrecondition`, mirroring the Stripe branch at `account.go:102`. Add `func (s *AccountService) SetPlay(p billing.PlayAPI)`.

- [ ] Write tests with a fake `PlayAPI` and fake `SubscriptionCanceler`:
  - `TestVerifyPlayPurchaseActivatesTrial`: the user becomes `trialing`, `billingProvider` is `play`, and acknowledge is called once.
  - `TestVerifyPlayPurchaseIdempotent`: calling twice acknowledges once.
  - `TestVerifyPlayPurchaseAccountMismatch`: returns `PermissionDenied`.
  - `TestVerifyPlayPurchaseTakesOverLapsedStripe`: Stripe cancel is called, then Play owns the user.
  - `TestVerifyPlayPurchaseTakeoverCancelFails`: `Unavailable`, no acknowledge, user unchanged.
  - `TestVerifyPlayPurchaseRejectsTestPurchase`.
  - `TestVerifyPlayPurchaseNotConfigured`.
  - `TestStripeTakeoverCancelsPlay`.
  - `TestDeleteAccountCancelsPlay`.
  - `TestPaywallExemptsVerifyPlayPurchase`.
- [ ] Run `go test ./internal/service ./internal/auth -run 'Play|Paywall|DeleteAccount'`. Expect FAIL.
- [ ] Implement.
- [ ] Run `task test` and expect PASS. Commit `feat: verify google play purchases`, and push.

### Task 5: RTDN endpoint and server wiring

**Files:**
- Create: `internal/billing/rtdn.go`, `internal/billing/rtdn_test.go`
- Modify: `cmd/server/main.go:166-202,245` (construct `Play` and `PlaySync` from env, register `BillingService`, call `SetPlay` on the auth and account services, mount the RTDN handler), `README.md` (env table), the design doc's links if paths changed

**Interfaces:**
- Consumes: `PlaySync.Sync(ctx, token, "")`.
- Produces: `type TokenValidator func(ctx context.Context, token, audience string) (*idtoken.Payload, error)` and `func NewRTDNHandler(log *zap.SugaredLogger, sync func(ctx context.Context, token string) error, validate TokenValidator, audience, serviceAccount, packageName string) http.Handler`. Behaviour:
  - Accept only POST.
  - Require `Authorization: Bearer <jwt>`. The payload must have the matching audience and claims `email == serviceAccount` and `email_verified == true`; otherwise 401.
  - Decode the Pub/Sub push envelope `{message:{data(base64),messageId}}` and the `DeveloperNotification` (`packageName`, `subscriptionNotification.purchaseToken`, `voidedPurchaseNotification.purchaseToken`, `testNotification`). A different packageName returns 400.
  - Test notification: log and return 204.
  - Otherwise call sync:
    - `ErrPlayPurchaseNotFound`, `ErrPlayAccountMismatch`, or an unknown user: log and return 204.
    - Any other error: 500.
    - Success: 204.
  - A nil sync (Play not configured) returns 503.
- `newHealthHandler` gains a parameter for the RTDN handler. It is a private function, so changing it is fine. Mount the handler at `/billing/play/rtdn`.
- `main.go`: build Play only when `PLAY_PACKAGE_NAME` is set. Log `google play billing disabled` otherwise.

- [ ] Write tests with a fake validator and fake sync:
  - `TestRTDNRejectsBadAudience` (401)
  - `TestRTDNRejectsWrongEmail` (401)
  - `TestRTDNSyncsSubscriptionToken` (sync called with the token, 204)
  - `TestRTDNVoidedPurchase`
  - `TestRTDNTestNotification`
  - `TestRTDNUnknownUserAcks` (204)
  - `TestRTDNTransientErrorRetries` (500)
  - `TestRTDNNotConfigured` (503)
  - `TestRTDNWrongPackage` (400)
- [ ] Run `go test ./internal/billing -run RTDN`. Expect FAIL.
- [ ] Implement and wire up.
- [ ] Run `task test`, `task lint`, and `go build ./...`. Expect PASS.
- [ ] Commit `feat: handle google play real-time notifications`, and push. Watch CI on #177 until it is green, then mark the PR ready.

### Task 6: icco.me infrastructure (separate PR, parallel with Tasks 1-5)

**Files (repo icco/icco.me, worktree `/data/workspace/.worktrees/icco.me/feat-etu-play-rtdn`):**
- Create: `etu_play.tf`
- Modify: `mist/docker-compose.yml` (`etu-backend` environment)

**Interfaces:**
- Produces: `google_project_service` for `androidpublisher.googleapis.com` and `pubsub.googleapis.com`. `google_pubsub_topic.etu_play_rtdn` (`etu-play-rtdn`). `google_pubsub_topic_iam_member` that grants `roles/pubsub.publisher` to `serviceAccount:google-play-developer-notifications@system.gserviceaccount.com`. `google_service_account.etu_play_rtdn_push` (`etu-play-rtdn-push`). `roles/iam.serviceAccountTokenCreator` on that account for the Pub/Sub service agent `service-<project_number>@gcp-sa-pubsub.iam.gserviceaccount.com`. `google_pubsub_subscription.etu_play_rtdn` with `push_config.push_endpoint = "https://be.etu.timeclimbers.com/billing/play/rtdn"` and `oidc_token { service_account_email = <push sa>, audience = same URL }`, `ack_deadline_seconds = 60`, and a retry policy of 10s to 600s. Constants go in `locals.tf`, following the repo's patterns. Compose env: `PLAY_PACKAGE_NAME: com.timeclimbers.etu`, `PLAY_RTDN_AUDIENCE`, and `PLAY_RTDN_SERVICE_ACCOUNT: etu-play-rtdn-push@icco-cloud.iam.gserviceaccount.com`.
- Read icco.me's AGENTS.md first. Edit YAML with `yq` and format with `yq -iP .`. Pin modules and providers as the repo does.

- [ ] Run `tofu fmt` and `tofu validate` (or whatever the repo uses), then the repo's yaml check (`go run ./cmd/check-yaml`).
- [ ] Commit `feat: add etu play rtdn pubsub`, push, and open a draft PR. Fix CI (tofu plan) until green. Do not merge or apply.

## After this plan

- Publish `@icco/etu-proto` (the repo's normal release flow, after #177 merges).
- etu-mobile PR (react-native-iap paywall, verify, Settings changes, link removal) and etu-web PR (Play status display, checkout guard, webhook treats `billing managed by google play` as final). Each gets a short task list written from the spec's Mobile and Web sections once the proto is published.
- Manual: Play Console product and base plans, Play API access for the `etu-backend` service account, the RTDN topic in Play Console, and `tofu apply`.
