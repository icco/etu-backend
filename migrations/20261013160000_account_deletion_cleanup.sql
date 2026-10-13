-- Create "AccountDeletionCleanup" table
CREATE TABLE "AccountDeletionCleanup" ("userId" text NOT NULL, "objectName" text NOT NULL, "createdAt" timestamptz NULL, PRIMARY KEY ("userId", "objectName"));
