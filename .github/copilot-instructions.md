# GitHub Copilot Instructions for etu-backend

## Project Overview

This is a gRPC-based notes and tags management API written in Go. The service provides a backend for managing user notes with tagging functionality, API key-based authentication, and optional Notion synchronization.

## Tech Stack

- **Go** 1.25
- **gRPC** for API communication
- **Protocol Buffers** for service definitions
- **PostgreSQL** for data persistence (via GORM)
- **Docker** for containerization
- **Task** for build automation

## Build, Test, and Lint Commands

Use Task for all build operations. The project uses Taskfile.yml for automation:

```bash
task build        # Build all binaries (server and sync)
task run          # Run the gRPC server
task test         # Run tests with coverage
task test-race    # Run tests with race detection
task lint         # Run linters (go vet, staticcheck)
task proto        # Regenerate protobuf files
task deps         # Install dependencies
```

### Important Build Notes

- Always run `task test` after making code changes
- Run `task proto` after modifying proto files
- Use `task lint` before committing code
- The build generates binaries in `bin/` directory
- **Build artifacts are excluded from version control** - binaries (`server`, `sync`) and the `bin/` directory are in `.gitignore`

## Go Coding Conventions

### General Guidelines

- Follow standard Go conventions and idiomatic patterns
- Use `gofmt` for code formatting
- Run `go vet` and `staticcheck` to catch common issues
- Keep functions small and focused
- Use descriptive variable names
- Add comments for exported functions and types

### Error Handling

- Always check and handle errors explicitly
- Use `fmt.Errorf` with `%w` verb for error wrapping
- Return errors instead of panicking
- Use gRPC status codes for service errors

Example:
```go
if err != nil {
    return nil, fmt.Errorf("failed to process note: %w", err)
}
```

### Context Usage

- Always pass `context.Context` as the first parameter
- Use `context.Background()` only in main functions or tests
- Propagate context through function calls
- Respect context cancellation in long-running operations

## gRPC and Protocol Buffers

### Proto File Guidelines

- Proto files are located in `proto/` directory
- After modifying `.proto` files, always run `task proto` to regenerate Go code
- Use Protocol Buffers v3 syntax
- Follow the existing naming patterns in `proto/etu.proto`

### gRPC Service Implementation

- Implement services in `internal/service/` package
- Each service method should:
  - Accept context as first parameter
  - Validate all input parameters
  - Return appropriate gRPC status codes
  - Handle errors gracefully

Example error patterns:
```go
if req.UserId == "" {
    return nil, status.Error(codes.InvalidArgument, "user_id is required")
}
```

Common status codes:
- `codes.InvalidArgument` - Bad request parameters
- `codes.NotFound` - Resource doesn't exist
- `codes.Unauthenticated` - Authentication failed
- `codes.Internal` - Internal server errors

## Database Patterns

### GORM Usage

- Database layer is in `internal/db/` package
- Models are defined in `internal/models/` package
- Use GORM for all database operations
- Always use context-aware methods (`WithContext`)

### Model Conventions

- All models use custom CUID-like IDs generated via `GenerateCUID()`
- Table names use PascalCase (e.g., "Note", "Tag", "ApiKey", "NoteTag")
- Column names use camelCase in struct tags (e.g., `userId`, `createdAt`)
- Use pointers for nullable fields
- Implement `TableName()` method for each model
- Use `BeforeCreate` hooks for ID generation

Example model structure:
```go
type Note struct {
    ID        string    `gorm:"column:id;primaryKey"`
    Content   string    `gorm:"column:content;type:text"`
    CreatedAt time.Time `gorm:"column:createdAt"`
    UpdatedAt time.Time `gorm:"column:updatedAt"`
    UserID    string    `gorm:"column:userId;index"`
}

func (Note) TableName() string {
    return "Note"
}
```

### Database Queries

- Use parameterized queries to prevent SQL injection
- Quote table/column names with double quotes for PostgreSQL: `"Note"`, `"userId"`
- Use GORM query builder methods for complex queries
- Always handle pagination with limit/offset
- Use indexes for foreign keys

Example:
```go
query := db.conn.WithContext(ctx).Model(&Note{}).Where(`"userId" = ?`, userID)
```

## Authentication

### API Key Format

- API keys use format: `etu_<64 hex characters>`
- Keys are stored hashed using bcrypt
- Key prefix (first 12 chars) is stored separately for lookup
- Authentication is handled in `internal/auth/` package

### Authentication in gRPC

- API keys are passed via gRPC metadata with key `authorization`
- All endpoints require authentication
- User ID is extracted from validated API key

## Testing

### Test Structure

- Test files are named `*_test.go`
- Located alongside the code they test
- Use table-driven tests for multiple scenarios
- Mock dependencies when appropriate

### Test Conventions

- Use Go's standard `testing` package
- Create mock implementations for testing services
- Test both success and error cases
- Use descriptive test names

Example:
```go
func TestCreateNote(t *testing.T) {
    // Test implementation
}
```

## Project Structure Guidelines

```
etu-backend/
├── cmd/
│   ├── server/          # gRPC server entry point
│   └── sync/            # Notion sync job entry point
├── internal/
│   ├── auth/            # API key authentication
│   ├── db/              # Database layer (GORM)
│   ├── models/          # Database models
│   ├── notion/          # Notion API client
│   ├── service/         # gRPC service implementations
│   ├── sync/            # Notion sync logic
│   └── syncdb/          # GORM database layer for sync
├── proto/               # Protocol buffer definitions
├── .github/workflows/   # CI/CD pipelines
└── Taskfile.yml         # Build automation
```

### Package Organization

- `cmd/` - Application entry points (main packages)
- `internal/` - Private application code
- `proto/` - Protocol buffer definitions and generated code
- Keep packages focused and avoid circular dependencies

## Environment Variables

Required for running the application:

- `DATABASE_URL` - PostgreSQL connection string (required)
- `PORT` - Server port (default: 50051)
- `NOTION_KEY` - Notion API key (required for sync job only)

## Docker

- Use the provided Dockerfile for containerization
- Multi-stage build pattern is used
- Base image is based on Go 1.25
- Pass environment variables via `-e` flag when running container

## Notion Sync

- Sync job is a separate binary in `cmd/sync`
- Uses GORM with auto-migration for database setup
- Syncs journal entries from Notion to PostgreSQL
- Supports full sync and incremental sync
- Can run as one-time job or continuously with interval

## Code Quality

### What to Avoid

- Don't use `panic` except in init functions or truly unrecoverable errors
- Avoid global mutable state
- Don't ignore errors (even in tests)
- Don't use naked returns in functions with named return values
- Avoid premature optimization
- **Never commit binary files or build artifacts** - use .gitignore to exclude them

### Best Practices

- Write self-documenting code with clear names
- Use interfaces for abstraction when appropriate
- Keep functions under 50 lines when possible
- Prefer composition over inheritance
- Use Go modules for dependency management
- Keep dependencies up to date

## Documentation

- Add comments for all exported functions, types, and constants
- Use godoc format for comments
- Keep README.md up to date with API changes
- Document complex algorithms or business logic

## Additional Notes

- The project uses PostgreSQL-specific features (e.g., ILIKE for case-insensitive search)
- Database schema is managed by the application layer (no migration files)
- GORM auto-migration runs on application startup
- Connection pooling is configured in `internal/db/db.go`
