FROM golang:1.26-bookworm AS builder

# Install task via go install to avoid supply-chain risk from curl|bash.
# The Go module proxy verifies the checksum of the downloaded module.
RUN go install github.com/go-task/task/v3/cmd/task@latest
RUN apt-get update && apt-get install -y git && apt-get clean && rm -rf /var/lib/apt/lists/*

WORKDIR /app

ENV GOOS=linux
ENV CGO_ENABLED=0

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN task build

# Final image
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*

# Create a non-root user.
RUN groupadd -r app && useradd -r -u 1001 -g app app

WORKDIR /app

COPY --from=builder --chown=app:app /app/bin/ /app/bin/

USER app

EXPOSE 8080 50051

CMD ["/app/bin/server"]