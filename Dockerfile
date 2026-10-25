# A dedicated ww-multiplayer server (docs/11-self-hosting.md): the
# `server` command alone, headless. It needs no game files, no Dolphin and
# no disc image, and the build context holds none (.dockerignore is an
# allowlist of the Go sources).
#
#   git checkout v0.5.0      # the players' release
#   docker build -t ww-multiplayer-server --build-arg VERSION=v0.5.0 .
#   echo "WW_OWNER_KEY=$(openssl rand -hex 16)" > ww-mp.env   # keep it: the key
#   docker run -d --name ww-mp --restart unless-stopped -p 25565:25565 \
#     --env-file ww-mp.env ww-multiplayer-server
#
# VERSION is only a label (the status probe and `version` report it): the
# protocol comes from the source checked out, so build from the players'
# release tag and pass that tag as VERSION (docs/10-lobby.md §6). With no
# WW_OWNER_KEY the server makes a key and prints it once (docker logs);
# each restart makes a new one. The
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
