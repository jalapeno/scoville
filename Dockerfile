# --- build stage -------------------------------------------------------------
FROM golang:1.26-alpine AS build

WORKDIR /src

# Cache module downloads separately from source so rebuilds are fast.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 produces a fully static binary safe for a scratch base.
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/scoville \
      ./cmd/scoville

# --- runtime stage -----------------------------------------------------------
FROM scratch

# TLS root CAs are needed for NATS TLS and any outbound HTTPS calls.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

COPY --from=build /out/scoville /scoville

# Default HTTP API port.
EXPOSE 8080

ENTRYPOINT ["/scoville"]
CMD ["--addr", ":8080"]
