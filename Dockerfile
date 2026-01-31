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
RUN task build
# Final image
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y ca-certificates && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=builder /app/bin/ /app/bin/

EXPOSE 8080 50051

CMD ["/app/bin/server"]