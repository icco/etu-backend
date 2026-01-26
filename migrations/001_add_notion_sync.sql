-- Migration: Add support for Notion sync
-- This migration adds the necessary columns and tables for syncing data from Notion

-- Add externalId column to Note table to store Notion page IDs
ALTER TABLE "Note" ADD COLUMN IF NOT EXISTS "externalId" TEXT;

-- Add notionUuid column to Note table to store the UUID from Notion's ID property
ALTER TABLE "Note" ADD COLUMN IF NOT EXISTS "notionUuid" TEXT;

-- Create indexes for efficient lookups
CREATE INDEX IF NOT EXISTS "Note_externalId_idx" ON "Note" ("externalId");
CREATE INDEX IF NOT EXISTS "Note_notionUuid_idx" ON "Note" ("notionUuid");
CREATE INDEX IF NOT EXISTS "Note_externalId_userId_idx" ON "Note" ("externalId", "userId");
CREATE INDEX IF NOT EXISTS "Note_notionUuid_userId_idx" ON "Note" ("notionUuid", "userId");

-- Create SyncState table to track last sync time per user
CREATE TABLE IF NOT EXISTS "SyncState" (
    "userId" TEXT NOT NULL PRIMARY KEY REFERENCES "User"(id) ON DELETE CASCADE,
    "lastSyncedAt" TIMESTAMP WITH TIME ZONE NOT NULL
);

-- Add unique constraints to prevent duplicate entries per user
CREATE UNIQUE INDEX IF NOT EXISTS "Note_externalId_userId_unique" ON "Note" ("externalId", "userId") WHERE "externalId" IS NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS "Note_notionUuid_userId_unique" ON "Note" ("notionUuid", "userId") WHERE "notionUuid" IS NOT NULL;
