# Database migrations

Schema changes are versioned SQL files in `migrations/`, managed with [Atlas](https://atlasgo.io/versioned/intro). GORM `AutoMigrate` no longer runs in deployed binaries.

## Deploy

The container's default command, `scripts/server-entrypoint.sh`, runs `atlas migrate apply` against `DATABASE_URL` and then starts the server. If the migration fails, the server does not start. Atlas takes a Postgres advisory lock during apply, so concurrent replicas are safe ([docs](https://atlasgo.io/versioned/apply)). The `sync` and `taggen` jobs never migrate.

## Changing the schema

1. Edit the models in `internal/models` (and the model list in `internal/db.AutoMigrate`).
2. Create two empty scratch databases and run `task migrate-diff NAME=<change>` with `ATLAS_MODELS_URL` and `ATLAS_DEV_URL` set to them, for example `postgres://postgres:postgres@localhost:5432/models?sslmode=disable&search_path=public`.
3. Review and commit the new file and `migrations/atlas.sum`.

CI (`.github/workflows/migrations.yml`) fails if the migrations directory drifts from the models, if `atlas.sum` is stale, or if `atlas migrate lint` reports an error.

## Baselining an existing database

Databases created by `AutoMigrate` already contain the tables in `migrations/20261013124430_baseline.sql`. Before the first deploy of this version, mark the baseline as applied without running it ([docs](https://atlasgo.io/versioned/apply#existing-databases)):

1. Confirm the live schema matches the baseline. This must print `Schemas are synced`:

   ```sh
   atlas schema diff \
     --from "$DATABASE_URL" \
     --to file://migrations \
     --dev-url "postgres://postgres:postgres@localhost:5432/dev?sslmode=disable&search_path=public"
   ```

   Append `search_path=public` to `DATABASE_URL` so only the application schema is compared. Use `?search_path=public` if the URL has no query string, otherwise `&search_path=public` (for example `postgres://u:p@host:5432/etu?sslmode=require&search_path=public`). Fix any reported drift with a reviewed manual change before continuing.

2. Record the baseline:

   ```sh
   atlas migrate apply --dir file://migrations --url "$DATABASE_URL" --baseline 20261013124430
   ```

3. `atlas migrate status --dir file://migrations --url "$DATABASE_URL"` should report `Already at latest version`.

Without step 2, `migrate apply` refuses to touch a non-empty database and the server exits at startup.
