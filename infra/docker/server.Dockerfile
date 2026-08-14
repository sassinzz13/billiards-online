# Build context is the repository root.
#
#   docker build -f infra/docker/server.Dockerfile .

FROM golang:1.26-alpine AS build

# Dependencies are downloaded in their own layer, so editing source does not re-download the
# module cache on every build.
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO_ENABLED=0 produces a static binary, which is what makes the scratch stage below possible.
# -trimpath keeps absolute build paths out of the binary; -s -w drops the symbol table and DWARF.
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/server \
      ./apps/server/cmd/server

# ---------------------------------------------------------------------------

# scratch: no shell, no package manager, no userland. Nothing to exploit and nothing to patch.
# The cost is that a container HEALTHCHECK has nothing to call, which is why the binary probes
# itself via `server -healthcheck`.
FROM scratch

# Needed for outbound TLS. Not used yet, but its absence is a confusing failure to debug later.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

COPY --from=build /out/server /server

# scratch has no /etc/passwd, so the user must be numeric. 65534 is nobody.
USER 65534:65534

EXPOSE 8080
ENTRYPOINT ["/server"]
