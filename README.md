# etu-backend

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
│   └── sync/            # Notion sync job entry point
├── internal/
│   ├── auth/            # API key authentication
│   ├── db/              # Database layer (PostgreSQL)
│   ├── notion/          # Notion API client
│   ├── service/         # gRPC service implementations
│   ├── sync/            # Notion-to-PostgreSQL sync logic
│   └── syncdb/          # GORM database layer for sync
├── migrations/          # SQL migration files
├── proto/               # Protocol buffer definitions
├── .github/workflows/   # CI/CD pipelines
├── Dockerfile
├── Taskfile.yml
└── go.mod
```

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

The sync job fetches journal entries from a Notion database and syncs them to PostgreSQL. It's designed to run as a cron job or continuously with an interval.

### Prerequisites

1. A Notion integration with access to your "Journal" database
2. Set the `NOTION_KEY` environment variable with your Notion API key
3. Run the database migration before first sync

### Database Migration

Before running the sync job for the first time, apply the migration:

```bash
psql $DATABASE_URL < migrations/001_add_notion_sync.sql
```

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
| `-migrate` | Run GORM auto-migrations before syncing | false |

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
