# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Install git and build essentials
RUN apk add --no-cache git ca-certificates tzdata

# Cache go modules
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build statically linked binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/bot ./cmd/bot

# Final runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/bot /app/bot
COPY --from=builder /app/config/config.yaml /app/config/config.yaml

# Set timezone
ENV TZ=Asia/Jakarta

ENTRYPOINT ["/app/bot"]
CMD ["-config", "/app/config/config.yaml"]
