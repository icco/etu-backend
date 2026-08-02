# CLAUDE.md

Go gRPC backend for Etu, a journaling product: notes + tags CRUD with PostgreSQL, GCS file attachments, and Gemini-powered AI features.

## Commands (Taskfile, run via `task <name>`)

- `task build` — build all binaries to `bin/` (server, sync, taggen)
- `task run` — run the gRPC server (`go run ./cmd/server`, port 50051)
- `task test` — `go test -v -cover ./...`
- `task test-race` — tests with race detector
- `task lint` — `go vet` + `staticcheck` (CI also runs golangci-lint)
- `task proto` — regenerate Go protobuf/gRPC code from `proto/etu.proto`
- `task proto-ts` — regenerate the TypeScript proto package (`packages/etu-proto`)
- `task deps` — `go mod download` + install protoc-gen-go / protoc-gen-go-grpc
- `task sync` / `task taggen` — run jobs locally (need `USER_ID`; taggen also needs `GEMINI_PROJECT` and ADC)

## Architecture

- `cmd/server` — gRPC server exposing 6 services defined in `proto/etu.proto`:
  NotesService, TagsService, AuthService, ApiKeysService, UserSettingsService, StatsService
- `cmd/sync` — Notion import job; `cmd/taggen` — AI tag-generation job (both per-user, optionally interval-looped)
- `internal/service` — gRPC service implementations
- `internal/db`, `internal/models` — PostgreSQL via GORM (`gorm.io/gorm`)
- `internal/auth` — dual auth: client API keys (`authorization: etu_<64 hex>` metadata) and M2M tokens (`GRPC_API_KEYS` env, comma-separated for rotation)
- `internal/storage` — Google Cloud Storage for image/audio attachments (`GCS_BUCKET`)
- `internal/ai`, `internal/tagging` — Gemini tag generation, OCR, audio transcription via `github.com/icco/gutil/vertex` on the Vertex AI backend. Auth is ADC; config is `GEMINI_PROJECT` and optional `GEMINI_LOCATION`. There is no API key.
- `internal/notion`, `internal/sync`, `internal/syncdb` — Notion sync pipeline
- `internal/crypto` — encryption; key from GCP Secret Manager (`GCP_SECRET_NAME`)
- `proto/` — `.proto` source plus generated `.pb.go` files (committed)

## Testing conventions

- Table-driven tests; DB layers tested with `go-sqlmock` (no live Postgres needed); comparisons via `go-cmp`
- Tests live next to code as `*_test.go`; shared fixtures in `testconsts_test.go`
- Run `task test` and `task lint` before committing; CI runs test, golangci-lint, and CodeQL workflows
