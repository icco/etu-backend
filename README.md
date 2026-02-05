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
- `NOTION_KEY` - Notion API key (for sync job)
- `GEMINI_API_KEY` - Gemini API key (for tag generation)
- `ENCRYPTION_KEY` - Base64-encoded 32-byte key for encrypting sensitive data at rest (optional but recommended for production)

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

## Security

### Encryption at Rest

Notion API keys stored in the database are encrypted using AES-256-GCM encryption. To enable encryption:

1. Generate a 32-byte (256-bit) encryption key:
```bash
openssl rand -base64 32
```

2. Set the `ENCRYPTION_KEY` environment variable:
```bash
export ENCRYPTION_KEY="<your-base64-encoded-key>"
```

**Important Notes:**
- If `ENCRYPTION_KEY` is not set, Notion keys will be stored in plaintext (not recommended for production)
- Keep your encryption key secure and backed up - losing it means losing access to encrypted data
- The system handles backwards compatibility: existing plaintext keys are automatically detected and can be decrypted after setting the encryption key
- Keys are encrypted before storage and decrypted when retrieved, transparently to application code

## License

This project uses a dual-license structure:

- **Backend code**: [CC-BY-NC-4.0](https://creativecommons.org/licenses/by-nc/4.0/) - See [LICENSE](LICENSE)
- **Proto package** (`packages/etu-proto/`): [MIT](packages/etu-proto/LICENSE)
