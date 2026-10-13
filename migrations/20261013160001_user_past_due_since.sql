-- Modify "User" table
ALTER TABLE "User" ADD COLUMN "pastDueSince" timestamptz NULL;
-- Start the grace period now for users already past due
UPDATE "User" SET "pastDueSince" = now() WHERE "subscriptionStatus" = 'past_due' AND "pastDueSince" IS NULL;
