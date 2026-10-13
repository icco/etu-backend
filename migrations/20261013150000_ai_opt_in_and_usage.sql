-- Modify "User" table
ALTER TABLE "User" ADD COLUMN "aiEnabled" boolean NOT NULL DEFAULT false;
-- Create "AIUsage" table
CREATE TABLE "AIUsage" ("userId" text NOT NULL, "day" date NOT NULL, "count" bigint NOT NULL DEFAULT 0, PRIMARY KEY ("userId", "day"));
