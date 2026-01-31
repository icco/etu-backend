FROM golang:1.25-bookworm AS builder

RUN curl -1sLf 'https://dl.cloudsmith.io/public/task/task/setup.deb.sh' | bash
RUN apt-get update && apt-get install -y task git && apt-get clean && rm -rf /var/lib/apt/lists/*

WORKDIR /app

ENV GOOS=linux
ENV CGO_ENABLED=0

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN go build -ldflags="-s -w" -o /bin/server ./cmd/server \
    && go build -ldflags="-s -w" -o /bin/sync ./cmd/sync
# Final image
FROM debian:bookworm-slim

WORKDIR /app

COPY --from=builder /app/bin/server /app/server

EXPOSE 8080 50051

CMD ["/app/server"]