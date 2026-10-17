# Google Play Billing for Etu

## Intent

The Android app links to the web subscription page, which likely violates Google Play's payments policy. Android users should be able to subscribe inside the app through Google Play Billing, while web users keep paying through Stripe. Both kinds of subscriber get the same access, enforced by the existing backend paywall (`internal/auth/paywall.go`).

## Goals

- A new Android user registers in the app, sees a paywall, and subscribes through Google Play with a 14-day free trial.
- The backend is the single source of truth for whether a user is paid, whichever store they paid through.
- Renewals, cancellations, grace periods, holds, refunds, and revocations reach the backend within minutes via Real-time developer notifications (RTDN).
- Users who already pay through Stripe keep full access and are never asked to buy through Play.
- Play subscribers see "Managed by Google Play" on the web, with a link to Play's subscription center, and are never offered Stripe checkout.

## Non-goals

- iOS / App Store billing. iOS has not shipped yet.
- Moving subscribers between Stripe and Play.
- Promo codes, upgrade or downgrade between plans beyond what Play handles natively, and family sharing.
- Any link from the mobile app to a web payment page.

## Anti-goals

- A user is charged by both Stripe and Play for the same account.
- A Stripe webhook overwrites a Play subscriber's status, or the reverse, and locks out a paying user.
- A purchase is granted without server-side verification against the Play Developer API.
- Purchases are left unacknowledged. Play refunds subscriptions that are not acknowledged within 3 days.

## Decisions (from the user)

- Path: register in the app, then the Play paywall. Paying before registering is not supported.
- Products: one Play subscription with two auto-renewing base plans: yearly at $5 and monthly at $1. Each base plan has a 14-day free-trial offer.
- Existing Stripe subscribers: full access, no Play prompt. Settings shows "Subscribed via web" with no link.
- Status updates: RTDN through a Pub/Sub push subscription to a new backend HTTP endpoint, which re-fetches the purchase from the Play Developer API.
- Mobile library: [react-native-iap](https://github.com/hyochan/react-native-iap).
- Rollout: etu-backend PR first (proto, migration, verification, RTDN), then etu-mobile and etu-web PRs once the new `@icco/etu-proto` is published. The icco.me infrastructure PR can be built alongside the backend PR.
- Switching stores cancels the old store's subscription first.

## Design

### Play Console (manual, done by the user)

- Subscription product ID `etu`, with base plans `yearly` ($5/year) and `monthly` ($1/month), each auto-renewing with a 14-day free-trial offer for new customers. (My call on IDs.)
- Enable the Google Play Android Developer API in the GCP project. Grant the backend's service account access in Play Console (view financial data, manage orders and subscriptions).
- Set the RTDN topic in Play Console > Monetization setup, and send a test notification.

### Infrastructure (icco.me PR)

etu-backend runs on `mist` via `mist/docker-compose.yml`. Caddy already proxies `be.etu.timeclimbers.com` to the HTTP server on port 8080, and the backend uses the `etu-backend` service account in `icco-cloud` (`locals.tf`). In icco.me Terraform:

- Enable `androidpublisher.googleapis.com` and `pubsub.googleapis.com`.
- Create Pub/Sub topic `etu-play-rtdn` and grant `roles/pubsub.publisher` to `google-play-developer-notifications@system.gserviceaccount.com`.
- Create a dedicated `etu-play-rtdn-push` service account, plus a push subscription to `https://be.etu.timeclimbers.com/billing/play/rtdn` that uses OIDC with that account and audience `https://be.etu.timeclimbers.com/billing/play/rtdn`.
- Add `PLAY_PACKAGE_NAME`, `PLAY_RTDN_AUDIENCE`, and `PLAY_RTDN_SERVICE_ACCOUNT` to the `etu-backend` service in `mist/docker-compose.yml`.

### Account linking

The app passes the Etu user ID as `obfuscatedAccountIdAndroid` when it launches the billing flow. The backend accepts a purchase only when `externalAccountIdentifiers.obfuscatedExternalAccountId` equals the calling user's ID. That way, RTDN events for tokens the backend has not seen yet can still be tied to a user. User IDs are opaque, not PII, and under Play's 64-character limit. (My call. If IDs can exceed 64 characters, use a SHA-256 hex digest and store it.)

### Data model (Atlas migration)

- New table `PlaySubscription`: `purchaseToken` (PK), `userId` (FK to User, cascade delete), `productId`, `basePlanId`, `state` (Play `SubscriptionState`), `expiryTime`, `autoRenewing`, `linkedPurchaseToken`, `acknowledged`, `createdAt`, `updatedAt`. Index on `userId`.
- New `User.billingProvider` column: nullable text, `'stripe'` or `'play'`.

### Provider ownership rule

Stripe and Play both write `subscriptionStatus`, `subscriptionEnd`, `cancelAtPeriodEnd`, and `pastDueSince`. A provider may write those fields only when `billingProvider` is NULL, already that provider, or the user's current status is not paid (not in `AIEligibleSubscriptionStatuses`, and not `past_due` within grace). A write that takes ownership sets `billingProvider`. The rule is enforced in the SQL `WHERE` clause, following the existing conditional-update pattern from icco/etu-backend#175. The migration sets `billingProvider = 'stripe'` for users who have a `stripeSubscriptionId`. (My call.)

Switching stores (user decision): before one provider takes ownership from the other, the backend cancels the other provider's subscription (Stripe: the existing `SubscriptionCanceler`; Play: `purchases.subscriptionsv2.cancel`) so it cannot recover and charge again. If the cancel fails, the takeover is refused. For a Play purchase that means `VerifyPlayPurchase` returns `Unavailable` and the app retries later without finishing the transaction. On the web, checkout is allowed for a Play-owned user only when they are not paid, and the backend cancels the Play subscription when the Stripe write takes over.

A Stripe write rejected because Play owns the user returns `FailedPrecondition` with the reason `billing managed by google play`. The etu-web Stripe webhook treats that error as final, the same way it treats a changed customer link, and stops retrying. (My call.) No user can be Play-owned until the mobile app ships, so the web change can follow the backend PR.

### Status mapping (Play `subscriptionState` to `subscriptionStatus`)

- `ACTIVE`, in a free-trial offer phase: `trialing`
- `ACTIVE`: `active`
- `CANCELED` (not yet expired): `active`, with `cancelAtPeriodEnd = true`
- `IN_GRACE_PERIOD`: `past_due`. `pastDueSince` is set when the user first enters the state, matching Stripe.
- `ON_HOLD`, `PAUSED`: `unpaid`
- `EXPIRED`, `PENDING_PURCHASE_CANCELED`, and revoked or voided purchases: `canceled`
- `PENDING`: no change

`subscriptionEnd` comes from the line item's `expiryTime`. Test purchases (`testPurchase` present) are accepted only when `PLAY_ALLOW_TEST_PURCHASES=true`.

Source: [purchases.subscriptionsv2](https://developers.google.com/android-publisher/api-ref/rest/v3/purchases.subscriptionsv2).

### Backend API

- New `BillingService.VerifyPlayPurchase(purchase_token, product_id)`, for API-key callers and exempt from the paywall. It calls `purchases.subscriptionsv2.get`, checks the package name, product ID, and obfuscated account ID, upserts `PlaySubscription`, applies the status mapping under the ownership rule, and acknowledges the purchase if it is pending. It returns the updated `User`. If `linkedPurchaseToken` is set, the old token's row is marked superseded. Calling it twice is safe.
- `User` proto gains `optional string billing_provider`, so mobile and web can tell how a subscriber pays.
- RTDN endpoint `POST /billing/play/rtdn` on the existing HTTP server. It verifies the Pub/Sub OIDC token (`google.golang.org/api/idtoken`, audience `PLAY_RTDN_AUDIENCE`, email `PLAY_RTDN_SERVICE_ACCOUNT`), decodes the `DeveloperNotification`, re-fetches the token, and applies the same update. It looks up the user by token, or else by the obfuscated account ID. It returns 2xx once the event is handled or permanently unmatched (logged), and 5xx on transient errors so Pub/Sub retries. `voidedPurchaseNotification` revokes access. `testNotification` is logged.
- `DeleteAccount` also cancels any active Play subscription (`purchases.subscriptionsv2.cancel`) before deleting rows, and aborts on failure, the same as for Stripe.
- New config: `PLAY_PACKAGE_NAME` (`com.timeclimbers.etu`), `PLAY_RTDN_AUDIENCE`, `PLAY_RTDN_SERVICE_ACCOUNT`, `PLAY_ALLOW_TEST_PURCHASES`. Play API credentials come from ADC. If the Play config is missing, the RPC returns `FailedPrecondition` and the endpoint returns 503. Startup does not fail.

### Mobile (etu-mobile)

- Add react-native-iap. The paywall screen lists both base plans with Play's localized prices and trial details, and launches the purchase with `obfuscatedAccountIdAndroid = user.id`.
- On a purchase update, call `VerifyPlayPurchase` and call `finishTransaction` only after the backend succeeds. On launch and on foreground, re-verify any purchases returned by `getAvailablePurchases` that are not acknowledged, to recover from crashes.
- Show the paywall right after registration when the user is unpaid, and whenever an RPC returns PermissionDenied "subscription required". Account deletion, export, logout, and settings stay reachable.
- In Settings, remove the "Manage subscription (web)" link. Play subscribers see "Manage in Google Play" (`https://play.google.com/store/account/subscriptions?sku=etu&package=com.timeclimbers.etu`). Stripe subscribers see "Subscribed via web" as text only. Unpaid users get a button that opens the paywall.
- iOS builds do not show the paywall's purchase buttons. They show "Subscriptions are not available on iOS yet".

### Web (etu-web)

- When `billing_provider` is `play` and the user is paid, Settings > Subscription shows "Managed by Google Play" with the Play link, and checkout and the billing portal are hidden. The checkout server action refuses with a clear message in that case.

## Left to the builder

- Package layout inside `internal/billing` and the Play API client wrapper (use `google.golang.org/api/androidpublisher/v3`).
- The exact proto message shapes beyond the fields above, and test fixtures for RTDN payloads.
- Mobile UI layout and copy, following the app's theme.
