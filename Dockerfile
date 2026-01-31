FROM golang:1.25-bookworm AS builder

RUN curl -1sLf 'https://dl.cloudsmith.io/public/task/task/setup.deb.sh' | bash
RUN apt update && apt install task git

WORKDIR /app

ENV GOOS=linux
ENV CGO_ENABLED=1

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN task build

# Final image
FROM debian:bookworm-slim

WORKDIR /app

COPY --from=builder /bin/ /app

EXPOSE 8080 50051

CMD ["/app/server"]
