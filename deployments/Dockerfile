# Build stage
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-s -w -X main.buildVersion=$(git describe --tags --always 2>/dev/null || echo docker) -X main.buildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o /bin/cockpit ./cmd/cockpit

# Runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata
RUN adduser -D -u 1000 cockpit

COPY --from=builder /bin/cockpit /usr/local/bin/cockpit
COPY internal/store/migrations /migrations

USER cockpit
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --retries=3 \
    CMD wget -qO- http://localhost:8080/health || exit 1

ENTRYPOINT ["cockpit"]
