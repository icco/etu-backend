-- Create "ApiKey" table
CREATE TABLE "ApiKey" (
  "id" text NOT NULL,
  "name" text NULL,
  "keyPrefix" text NULL,
  "keyHash" text NULL,
  "userId" text NULL,
  "createdAt" timestamptz NULL,
  "lastUsed" timestamptz NULL,
  PRIMARY KEY ("id")
);
-- Create index "idx_ApiKey_user_id" to table: "ApiKey"
CREATE INDEX "idx_ApiKey_user_id" ON "ApiKey" ("userId");
-- Create "Note" table
CREATE TABLE "Note" (
  "id" text NOT NULL,
  "content" text NULL,
  "createdAt" timestamptz NULL,
  "updatedAt" timestamptz NULL,
  "userId" text NULL,
  "externalId" text NULL,
  "notionUuid" text NULL,
  "lastSyncedToNotion" timestamptz NULL,
  PRIMARY KEY ("id")
);
-- Create index "idx_Note_external_id" to table: "Note"
CREATE INDEX "idx_Note_external_id" ON "Note" ("externalId");
-- Create index "idx_Note_notion_uuid" to table: "Note"
CREATE INDEX "idx_Note_notion_uuid" ON "Note" ("notionUuid");
-- Create index "idx_Note_user_id" to table: "Note"
CREATE INDEX "idx_Note_user_id" ON "Note" ("userId");
-- Create "NoteTag" table
CREATE TABLE "NoteTag" (
  "noteId" text NOT NULL,
  "tagId" text NOT NULL,
  PRIMARY KEY ("noteId", "tagId")
);
-- Create "SyncState" table
CREATE TABLE "SyncState" (
  "userId" text NOT NULL,
  "lastSyncedAt" timestamptz NULL,
  PRIMARY KEY ("userId")
);
-- Create "User" table
CREATE TABLE "User" (
  "id" text NOT NULL,
  "email" text NULL,
  "name" text NULL,
  "passwordHash" text NULL,
  "subscriptionStatus" text NULL,
  "subscriptionEnd" timestamptz NULL,
  "createdAt" timestamptz NULL,
  "stripeCustomerId" text NULL,
  "stripeSubscriptionId" text NULL,
  "stripePriceId" text NULL,
  "cancelAtPeriodEnd" boolean NULL DEFAULT false,
  "currentPeriodStart" timestamptz NULL,
  "billingLine1" text NULL,
  "billingLine2" text NULL,
  "billingCity" text NULL,
  "billingState" text NULL,
  "billingPostalCode" text NULL,
  "billingCountry" text NULL,
  "notionKey" text NULL,
  "notionDatabaseName" text NULL,
  "profileImageGCSObject" text NULL,
  "updatedAt" timestamptz NULL,
  "disabled" boolean NULL DEFAULT false,
  "disabledReason" text NULL,
  "failedLoginAttempts" bigint NULL DEFAULT 0,
  "lastFailedLogin" timestamptz NULL,
  PRIMARY KEY ("id")
);
-- Create "NoteAudio" table
CREATE TABLE "NoteAudio" (
  "id" text NOT NULL,
  "noteId" text NOT NULL,
  "url" text NOT NULL,
  "gcsObjectName" text NOT NULL,
  "transcribedText" text NULL,
  "mimeType" text NULL,
  "createdAt" timestamptz NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_Note_audios" FOREIGN KEY ("noteId") REFERENCES "Note" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- Create index "idx_NoteAudio_note_id" to table: "NoteAudio"
CREATE INDEX "idx_NoteAudio_note_id" ON "NoteAudio" ("noteId");
-- Create "NoteImage" table
CREATE TABLE "NoteImage" (
  "id" text NOT NULL,
  "noteId" text NOT NULL,
  "url" text NOT NULL,
  "gcsObjectName" text NOT NULL,
  "extractedText" text NULL,
  "mimeType" text NULL,
  "createdAt" timestamptz NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "fk_Note_images" FOREIGN KEY ("noteId") REFERENCES "Note" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- Create index "idx_NoteImage_note_id" to table: "NoteImage"
CREATE INDEX "idx_NoteImage_note_id" ON "NoteImage" ("noteId");
-- Create "Tag" table
CREATE TABLE "Tag" (
  "id" text NOT NULL,
  "name" text NULL,
  "createdAt" timestamptz NULL,
  "userId" text NULL,
  "count" bigint NULL,
  PRIMARY KEY ("id")
);
-- Create index "idx_Tag_user_id" to table: "Tag"
CREATE INDEX "idx_Tag_user_id" ON "Tag" ("userId");
-- Create "note_tags" table
CREATE TABLE "note_tags" (
  "note_id" text NOT NULL,
  "tag_id" text NOT NULL,
  PRIMARY KEY ("note_id", "tag_id"),
  CONSTRAINT "fk_note_tags_note" FOREIGN KEY ("note_id") REFERENCES "Note" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "fk_note_tags_tag" FOREIGN KEY ("tag_id") REFERENCES "Tag" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION
);
