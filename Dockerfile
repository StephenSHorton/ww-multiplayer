# A dedicated ww-multiplayer server (docs/11-self-hosting.md): the
# `server` command alone, headless. It needs no game files, no Dolphin and
# no disc image, and the build context holds none (.dockerignore is an
# allowlist of the Go sources).
#
#   docker build -t ww-multiplayer-server --build-arg VERSION=v0.5.0 .
#   docker run -d --name ww-mp --restart unless-stopped -p 25565:25565 \
#     -e WW_OWNER_KEY="$(openssl rand -hex 16)" ww-multiplayer-server
#
# VERSION must be the players' release tag: the status probe reports it,
# and every player needs the same protocol (docs/10-lobby.md §6). The
# server listens on 25565 inside the container and the health check asks
# there; to use another port outside, change only the left side of -p
# (-p 7000:25565).

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/ww-multiplayer ./cmd/ww-multiplayer

# distroless static: no shell, no package manager, a non-root user
# (65532). The binary is the health check too (`status`: the TCP status
# probe on the game port), so the image needs no wget or curl.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/ww-multiplayer /ww-multiplayer
USER nonroot:nonroot
EXPOSE 25565/tcp
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD ["/ww-multiplayer", "status", "--timeout", "3s", "127.0.0.1:25565"]
# `docker stop` sends SIGTERM: the server stops cleanly (RunServer).
ENTRYPOINT ["/ww-multiplayer", "server"]
