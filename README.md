# etu-backend

[![Go Reference](https://pkg.go.dev/badge/github.com/icco/etu-backend.svg)](https://pkg.go.dev/github.com/icco/etu-backend)
[![CI](https://github.com/icco/etu-backend/actions/workflows/ci.yml/badge.svg)](https://github.com/icco/etu-backend/actions/workflows/ci.yml)

A gRPC-based notes and tags management API written in Go. This service provides a backend for managing user notes with tagging functionality and API key-based authentication.

## Features

- Create, read, update, and delete notes
- Tag notes with multiple labels
- Search and filter notes by tags, date ranges, and content
- Manage and view all tags with usage counts
- API key authentication
- PostgreSQL database backend

## Tech Stack

- **Go** 1.25
- **gRPC** for API communication
- **Protocol Buffers** for service definitions
- **PostgreSQL** for data persistence
- **Docker** for containerization
- **Task** for build automation

## Project Structure

```
etu-backend/
├── cmd/
│   ├── server/          # gRPC API server entry point
│   ├── sync/            # Notion sync job entry point
│   └── taggen/          # AI tag generation job entry point
├── internal/
│   ├── ai/              # AI integration (Gemini)
│   ├── auth/            # API key authentication
│   ├── db/              # Database layer (PostgreSQL)
│   ├── models/          # Shared data models
│   ├── notion/          # Notion API client
│   ├── service/         # gRPC service implementations
│   ├── sync/            # Notion-to-PostgreSQL sync logic
│   └── syncdb/          # GORM database layer for sync
├── proto/               # Protocol buffer definitions
├── packages/etu-proto/  # TypeScript proto package
├── .github/workflows/   # CI/CD pipelines
├── Dockerfile
├── Taskfile.yml
└── go.mod
```

## Documentation

Full API documentation is available on [pkg.go.dev](https://pkg.go.dev/github.com/icco/etu-backend).

## Prerequisites

- Go 1.25+
- PostgreSQL
- [Task](https://taskfile.dev/) (optional, for build automation)
- [protoc](https://grpc.io/docs/protoc-installation/) (for regenerating proto files)

## Environment Variables

| Variable | Description | Required |
|----------|-------------|----------|
| `DATABASE_URL` | PostgreSQL connection string | Yes |
| `PORT` | Server port (default: 50051) | No |
| `NOTION_KEY` | Notion API key (for sync job) | Sync only |
| `GEMINI_API_KEY` | Gemini API key (for tag generation) | Taggen only |

## Getting Started

### Install Dependencies

```bash
task deps
# or
go mod download
```

### Run the Server

```bash
task run
# or
go run ./cmd/server
```

### Build

```bash
task build
# Binary output: bin/server
```

## Docker

### Build Image

```bash
docker build -t etu-backend .
```

### Run Container

```bash
docker run -e DATABASE_URL="postgres://user:pass@host:5432/dbname?sslmode=disable" -p 50051:50051 etu-backend
```

## API

The server exposes two gRPC services on port 50051.

### Authentication

All endpoints require an API key in the gRPC metadata:

```
authorization: etu_<64 hex characters>
```

### NotesService

| Method | Description |
|--------|-------------|
| `ListNotes` | Get paginated list of notes with optional filtering (search, tags, date range) |
| `CreateNote` | Create a new note with tags |
| `GetNote` | Retrieve a single note by ID |
| `UpdateNote` | Update note content and/or tags |
| `DeleteNote` | Delete a note by ID |

### TagsService

| Method | Description |
|--------|-------------|
| `ListTags` | Get all tags for a user with usage counts |

See [`proto/etu.proto`](proto/etu.proto) for full API definitions.

## Development

### Generate Proto Files

```bash
task proto
```

### Run Tests

```bash
task test        # Run tests with coverage
task test-race   # Run tests with race detection
```

### Lint

```bash
task lint
```

### Available Tasks

```bash
task --list      # List all available tasks
```

## Notion Sync Job

The sync job fetches journal entries from a Notion database and syncs them to PostgreSQL. It's designed to run as a cron job or continuously with an interval. Database tables are managed automatically by GORM on startup.

### Prerequisites

1. A Notion integration with access to your "Journal" database
2. Set the `NOTION_KEY` environment variable with your Notion API key

### Running the Sync Job

**One-time sync:**
```bash
./bin/sync -user <USER_ID> -full
```

**Incremental sync (only changes since last sync):**
```bash
./bin/sync -user <USER_ID>
```

**Continuous sync (every hour):**
```bash
./bin/sync -user <USER_ID> -interval 1h
```

### Command Line Flags

| Flag | Description | Default |
|------|-------------|---------|
| `-user` | User ID to sync notes for (required) | - |
| `-full` | Perform a full sync instead of incremental | false |
| `-interval` | Run continuously with this interval (e.g., `1h`, `30m`) | - |

### Example Cron Entry

To sync every hour:

```cron
0 * * * * DATABASE_URL="..." NOTION_KEY="..." /path/to/sync -user <USER_ID> >> /var/log/etu-sync.log 2>&1
```

### Data Mapping

The sync job maps Notion data to PostgreSQL as follows:

| Notion Field | PostgreSQL Column | Description |
|--------------|-------------------|-------------|
| Page ID | `externalId` | Notion's page identifier |
| ID property | `notionUuid` | UUID stored in the "ID" title property |
| Page content | `content` | Paragraph blocks as text |
| Tags property | Tags via `NoteTag` | Multi-select tags |
| Created time | `createdAt` | Page creation timestamp |
| Last edited | `updatedAt` | Page modification timestamp |

## Gemini Tag Generation Job

The tag generation job uses Google's Gemini AI to automatically generate tags for notes that have fewer than 3 tags. It's designed to run as a cron job or continuously with an interval.

### Features

- Generates up to 3 tags per note using Gemini 1.5 Flash (cost-effective)
- Only processes notes with fewer than 3 tags
- Prefers reusing existing tags over creating new ones
- All tags are lowercase and single words (no spaces or hyphens)
- Never modifies or deletes existing tags

### Prerequisites

1. Set the `GEMINI_API_KEY` environment variable with your Google AI API key
2. Get your API key from [Google AI Studio](https://aistudio.google.com/app/apikey)

### Running the Tag Generation Job

**One-time generation:**
```bash
./bin/taggen -user <USER_ID>
```

**Dry run (test without adding tags):**
```bash
./bin/taggen -user <USER_ID> -dry-run
```

**Continuous generation (every 6 hours):**
```bash
./bin/taggen -user <USER_ID> -interval 6h
```

### Command Line Flags

| Flag | Description | Default |
|------|-------------|---------|
| `-user` | User ID to generate tags for (required) | - |
| `-dry-run` | Run without actually adding tags (for testing) | false |
| `-delay` | Delay between processing notes to avoid rate limiting (e.g., `2s`, `5s`) | 2s |
| `-interval` | Run continuously with this interval (e.g., `6h`, `1h`) | - |

### Example Cron Entry

To generate tags every 6 hours:

```cron
0 */6 * * * DATABASE_URL="..." GEMINI_API_KEY="..." /path/to/taggen -user <USER_ID> >> /var/log/etu-taggen.log 2>&1
```

### How It Works

1. Fetches all notes for the user that have fewer than 3 tags
2. For each note:
   - Sends the note content to Gemini 1.5 Flash
   - Receives up to 3 tag suggestions
   - Validates tags (lowercase, single word, alphanumeric only)
   - Prefers existing tags to maintain consistency
   - Adds only the number of tags needed to reach 3 total
   - Updates the note's `updatedAt` timestamp
3. Waits for the configured delay (default 2s) between requests to avoid rate limiting
