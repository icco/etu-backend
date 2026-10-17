-- Modify "User" table
ALTER TABLE "User" ADD COLUMN "billingProvider" text NULL;
-- Create "PlaySubscription" table
CREATE TABLE "PlaySubscription" (
  "purchaseToken" text NOT NULL,
  "userId" text NOT NULL,
  "productId" text NOT NULL,
  "basePlanId" text NOT NULL,
  "state" text NOT NULL,
  "expiryTime" timestamptz NULL,
  "autoRenewing" boolean NOT NULL DEFAULT false,
  "acknowledged" boolean NOT NULL DEFAULT false,
  "superseded" boolean NOT NULL DEFAULT false,
  "linkedPurchaseToken" text NULL,
  "createdAt" timestamptz NOT NULL,
  "updatedAt" timestamptz NOT NULL,
  PRIMARY KEY ("purchaseToken"),
  CONSTRAINT "fk_PlaySubscription_User" FOREIGN KEY ("userId") REFERENCES "User" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Create index "idx_PlaySubscription_user_id" to table: "PlaySubscription"
CREATE INDEX "idx_PlaySubscription_user_id" ON "PlaySubscription" ("userId");
UPDATE "User" SET "billingProvider" = 'stripe' WHERE "stripeSubscriptionId" IS NOT NULL;
