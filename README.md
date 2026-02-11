# etu-backend

[![Go Reference](https://pkg.go.dev/badge/github.com/icco/etu-backend.svg)](https://pkg.go.dev/github.com/icco/etu-backend)
[![CI](https://github.com/icco/etu-backend/actions/workflows/ci.yml/badge.svg)](https://github.com/icco/etu-backend/actions/workflows/ci.yml)

A gRPC-based notes and tags management API written in Go. This service provides a backend for managing user notes with tagging functionality and API key-based authentication.

## Clients

- **[etu-web](https://github.com/icco/etu-web)** - Web frontend for etu-backend
- **[etu](https://github.com/icco/etu)** - Command-line client for etu-backend

## Features

- CRUD operations for notes with tagging
- Image and audio file attachments for notes
- Search and filter by tags, date ranges, and content
- API key authentication with PostgreSQL backend
- Notion sync job for importing journal entries
- AI-powered features using Google Gemini:
  - Automatic tag generation for notes
  - OCR text extraction from images
  - Audio transcription

**Tech Stack:** Go 1.25, gRPC, Protocol Buffers, PostgreSQL, Docker

## Quick Start

**Prerequisites:** Go 1.25+, PostgreSQL, [Task](https://taskfile.dev/) (optional)

**Environment Variables:**
- `DATABASE_URL` - PostgreSQL connection string (required)
- `PORT` - Server port (default: 50051)
- `GRPC_API_KEYS` - Comma-separated list of M2M tokens for server-to-server auth (supports rotation)
- `GEMINI_API_KEY` - Gemini API key (for AI processing: tag generation, OCR, audio transcription)
- `GCS_BUCKET` - Google Cloud Storage bucket name (for image and audio file access)
- `GCP_SECRET_NAME` - GCP Secret Manager secret name for encryption key (required for encryption, format: `projects/PROJECT_ID/secrets/SECRET_NAME/versions/VERSION`)

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
- `GRPC_API_KEYS` - Comma-separated list of valid M2M tokens

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

## AI Processing Job

Automatically processes notes using Google Gemini AI for three tasks:

1. **Tag Generation**: Generates up to 3 tags per note (only for notes with fewer than 3 tags)
2. **Image OCR**: Extracts text from uploaded images
3. **Audio Transcription**: Transcribes uploaded audio files

Requires `GEMINI_API_KEY` from [Google AI Studio](https://aistudio.google.com/app/apikey) and `GCS_BUCKET` for accessing uploaded files.

**Usage:**
```bash
./bin/taggen                        # One-time processing
./bin/taggen -dry-run               # Test without updating database
./bin/taggen -interval 6h           # Continuous (every 6 hours)
```

**Flags:** `-dry-run`, `-delay` (default: 2s), `-interval` (e.g., `6h`, `1h`)

**Features:**
- **Tag Generation**: Prefers reusing existing tags, all tags are lowercase single words, never modifies existing tags
- **OCR**: Processes images uploaded to notes where `extractedText` is empty
- **Audio Transcription**: Processes audio files uploaded to notes where `transcribedText` is empty
- **Rate Limiting**: Configurable delay between API calls to avoid rate limits
- All three tasks run in sequence during each processing cycle

## Security

### Encryption at Rest

Notion API keys stored in the database are encrypted using AES-256-GCM encryption. The encryption key must be stored in GCP Secret Manager.

#### Setup

1. Create a secret in GCP Secret Manager:
```bash
# Generate encryption key
KEY=$(openssl rand -base64 32)

# Create secret in GCP
echo -n "$KEY" | gcloud secrets create notion-encryption-key --data-file=-
```

2. Set the `GCP_SECRET_NAME` environment variable:
```bash
export GCP_SECRET_NAME="projects/YOUR_PROJECT_ID/secrets/notion-encryption-key/versions/latest"
```

Your application must have permissions to access GCP Secret Manager (e.g., via service account or workload identity).

**Important Notes:**
- `GCP_SECRET_NAME` is required for encryption functionality
- If not configured, Notion keys will be stored in plaintext (not recommended for production)
- Keep your encryption key secure and backed up - losing it means losing access to encrypted data
- The encryption key is cached after first retrieval for performance
- **Migration**: Existing plaintext keys will continue to work after enabling encryption. They will remain unencrypted in the database until users update their settings (e.g., by re-saving their Notion key). The system detects plaintext keys during retrieval and handles them transparently.
- Keys are encrypted before storage and decrypted when retrieved, transparently to application code

## License

This project uses a dual-license structure:

- **Backend code**: [CC-BY-NC-4.0](https://creativecommons.org/licenses/by-nc/4.0/) - See [LICENSE](LICENSE)
- **Proto package** (`packages/etu-proto/`): [MIT](packages/etu-proto/LICENSE)
