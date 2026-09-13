# AGENTS.md

Guidance for coding agents working on etu-backend.
See [CLAUDE.md](CLAUDE.md) for Claude Code entrypoint (`@AGENTS.md`).

## Project Overview

Go gRPC backend for Etu, a journaling product: notes and tags CRUD with PostgreSQL, GCS attachments, and Gemini-powered AI features.

## Commands (Taskfile)

Run via `task <name>`:
- `task build` — Build all binaries to `bin/` (`server`, `sync`, `taggen`)
- `task run` — Run gRPC server (`go run ./cmd/server`, port 50051)
- `task test` / `task test-race` — Run unit tests (with race detector)
- `task lint` — Run `go vet` + `staticcheck` (CI also runs `golangci-lint`)
- `task proto` — Regenerate Go protobuf/gRPC code from `proto/etu.proto`
- `task proto-ts` — Regenerate TypeScript proto package (`packages/etu-proto`)
- `task deps` — Download modules and install proto tools

## Architecture & Layout

- `cmd/server` — gRPC server exposing 6 services defined in `proto/etu.proto` (Notes, Tags, Auth, ApiKeys, UserSettings, Stats).
- `cmd/sync` / `cmd/taggen` — Per-user background sync and AI tag-generation jobs.
- `internal/service` — gRPC service implementations.
- `internal/db`, `internal/models` — PostgreSQL via GORM.
- `internal/auth` — Client API keys (`etu_<64 hex>` metadata) & M2M tokens (`GRPC_API_KEYS`).
- `internal/storage` — Google Cloud Storage for media attachments (`GCS_BUCKET`).
- `internal/ai`, `internal/tagging` — Gemini features via `github.com/icco/gutil/vertex` on Vertex AI (ADC auth via `GEMINI_PROJECT`; no API key).
- `proto/` — Source `.proto` files and committed generated `.pb.go` code.

## Conventions

- Table-driven unit tests with `go-sqlmock` and `go-cmp`.
- Conventional Commits with lowercase subjects.
- Run `task test` and `task lint` before committing.
