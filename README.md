# etu-backend

[![Go Reference](https://pkg.go.dev/badge/github.com/icco/etu-backend.svg)](https://pkg.go.dev/github.com/icco/etu-backend)
[![CI](https://github.com/icco/etu-backend/actions/workflows/ci.yml/badge.svg)](https://github.com/icco/etu-backend/actions/workflows/ci.yml)

A gRPC-based notes and tags management API written in Go. This service provides a backend for managing user notes with tagging functionality and API key-based authentication.

## Clients

- **[etu-web](https://github.com/icco/etu-web)** - Web frontend for etu-backend
- **[etu](https://github.com/icco/etu)** - Command-line client for etu-backend

## Features

- CRUD operations for notes with tagging
- Search and filter by tags, date ranges, and content
- API key authentication with PostgreSQL backend
- Notion sync job for importing journal entries
- AI tag generation using Google Gemini

**Tech Stack:** Go 1.25, gRPC, Protocol Buffers, PostgreSQL, Docker

## Quick Start

**Prerequisites:** Go 1.25+, PostgreSQL, [Task](https://taskfile.dev/) (optional)

**Environment Variables:**
- `DATABASE_URL` - PostgreSQL connection string (required)
- `PORT` - Server port (default: 50051)
- `GRPC_API_KEYS` - Comma-separated list of M2M tokens for server-to-server auth (supports rotation)
- `GRPC_API_KEY` - Single M2M token (deprecated, use `GRPC_API_KEYS` instead)
- `NOTION_KEY` - Notion API key (for sync job)
- `GEMINI_API_KEY` - Gemini API key (for tag generation)

**Run locally:**
```bash
task deps    # or: go mod download
task run     # or: go run ./cmd/server
```

**With Docker:**
```bash
docker build -t etu-backend .
docker run -e DATABASE_URL="postgres://..." -p 50051:50051 etu-backend
```

## API

The server exposes gRPC services on port 50051. Full API documentation: [pkg.go.dev](https://pkg.go.dev/github.com/icco/etu-backend)

**Authentication:** All endpoints require an API key in gRPC metadata:
```
authorization: etu_<64 hex characters>
```

**NotesService:** `ListNotes`, `CreateNote`, `GetNote`, `UpdateNote`, `DeleteNote`, `GetRandomNotes`  
**TagsService:** `ListTags`

Search is performed via `ListNotes` with the `search` field (case-insensitive substring match on content). Can be combined with filters: `tags`, `start_date`, `end_date`, `limit`, `offset`.

See [`proto/etu.proto`](proto/etu.proto) for full definitions.

## Machine-to-Machine (M2M) Authentication

For server-to-server authentication (e.g., between `etu-web` and `etu-backend`), use M2M tokens passed via the `authorization` metadata header.

**Configuration:**
- `GRPC_API_KEYS` (recommended) - Comma-separated list of valid M2M tokens
- `GRPC_API_KEY` (deprecated) - Single M2M token (for backwards compatibility)

**Token Rotation Procedure:**

To rotate M2M tokens without downtime:

1. Generate a new secret token (e.g., using `openssl rand -hex 32`)
2. Add the new token to `GRPC_API_KEYS` alongside the old token:
   ```bash
   GRPC_API_KEYS="new_token_here,old_token_here"
   ```
3. Deploy the backend with both tokens active
4. Update clients (e.g., `etu-web`) to use the new token
5. After all clients are updated, remove the old token from `GRPC_API_KEYS`
6. Deploy the backend with only the new token

**Migration from deprecated single-token:**

If you're currently using `GRPC_API_KEY`, migrate to `GRPC_API_KEYS`:

```bash
# Old (deprecated)
GRPC_API_KEY="your_token"

# New (recommended)
GRPC_API_KEYS="your_token"
```

The system will log a deprecation warning if using `GRPC_API_KEY`.

## Development

```bash
task proto       # Generate proto files (requires protoc)
task test        # Run tests with coverage
task test-race   # Run tests with race detection
task lint        # Run linters
task --list      # List all available tasks
```

## Notion Sync Job

Syncs journal entries from a Notion database to PostgreSQL. Automatically syncs all users with Notion API keys configured.

**Usage:**
```bash
./bin/sync -full                    # One-time full sync
./bin/sync                          # Incremental sync
./bin/sync -interval 1h             # Continuous sync (hourly)
./bin/sync -direction to-notion     # Sync from PostgreSQL to Notion
./bin/sync -direction bidirectional # Two-way sync
```

**Flags:** `-full`, `-interval` (e.g., `1h`, `30m`), `-direction` (from-notion, to-notion, bidirectional)

## AI Tag Generation Job

Automatically generates up to 3 tags per note using Google Gemini 1.5 Flash. Only processes notes with fewer than 3 tags. Requires `GEMINI_API_KEY` from [Google AI Studio](https://aistudio.google.com/app/apikey).

**Usage:**
```bash
./bin/taggen                        # One-time generation
./bin/taggen -dry-run               # Test without adding tags
./bin/taggen -interval 6h           # Continuous (every 6 hours)
```

**Flags:** `-dry-run`, `-delay` (default: 2s), `-interval` (e.g., `6h`, `1h`)

**Features:** Prefers reusing existing tags, all tags are lowercase single words, never modifies existing tags.

## License

This project uses a dual-license structure:

- **Backend code**: [CC-BY-NC-4.0](https://creativecommons.org/licenses/by-nc/4.0/) - See [LICENSE](LICENSE)
- **Proto package** (`packages/etu-proto/`): [MIT](packages/etu-proto/LICENSE)
