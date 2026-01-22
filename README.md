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
├── cmd/server/          # Application entry point
├── internal/
│   ├── auth/            # API key authentication
│   ├── db/              # Database layer (PostgreSQL)
│   └── service/         # gRPC service implementations
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
