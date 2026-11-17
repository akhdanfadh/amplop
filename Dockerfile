# syntax=docker/dockerfile:1

# The inbok image: the Go binary with the built web app inside, on a
# distroless base with no shell or package manager. Build it with
# `make image`; backend/README.md explains running it.

# Base images, pinned by version and by digest so every build of a commit
# uses the same bytes. To update one, change its tag and look up the new
# digest, e.g.:
#   docker buildx imagetools inspect golang:1.27.3-trixie --format '{{.Manifest.Digest}}'
# Keep pnpm's version equal to devEngines.packageManager in
# web/package.json; otherwise pnpm downloads that version itself on every
# build.
ARG PNPM_IMAGE=ghcr.io/pnpm/pnpm:12.8.1@sha256:44cfd11d75b901dc7a83a16812d9134e4bf9d82f6b0eb28af6d666ebbf0fb4fb
ARG GO_IMAGE=golang:1.27.3-trixie@sha256:c65ecfe613b539ac74c62a4c3b2c27e8f8a6076384c8f27e8a3c0f3be17cc385
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian13:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329

# Web app: install dependencies before copying the sources, so a source
# change reuses the installed packages. The image has pnpm but no Node:
# pnpm installs the Node that web/pnpm-lock.yaml pins, checked against the
# lockfile's hash, as it does on a developer's machine.
FROM ${PNPM_IMAGE} AS web
WORKDIR /src/web
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
COPY web/patches/ ./patches/
# Cache ids are shared by every build on the machine; inbok's own id keeps
# its package store apart from other projects'.
RUN --mount=type=cache,id=inbok-pnpm,target=/var/cache/pnpm \
    pnpm install --frozen-lockfile --store-dir /var/cache/pnpm
COPY web/ ./
# The build pre-renders the shell through a preview server on localhost.
# Here localhost resolves to ::1 first, so the server listens there while
# Vite hands out a 127.0.0.1 URL, and the request is refused.
RUN NODE_OPTIONS=--dns-result-order=ipv4first pnpm build

# Go binary. Downloaded modules and compiled packages live in cache mounts
# that every build reuses, so no separate download step is needed: a source
# change recompiles only what changed. The web build is copied in last, so
# a backend-only change reuses the cached web stage. CGO off gives a static
# binary for distroless.
FROM ${GO_IMAGE} AS backend
WORKDIR /src/backend
ENV CGO_ENABLED=0 GOTOOLCHAIN=local
COPY backend/ ./
COPY --from=web /src/web/dist/client/ internal/webapp/dist/
RUN --mount=type=cache,id=inbok-gomod,target=/go/pkg/mod \
    --mount=type=cache,id=inbok-gobuild,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/inbok ./cmd/inbok

FROM ${RUNTIME_IMAGE}
COPY --from=backend /out/inbok /usr/local/bin/inbok
# Release mode stops Gin printing its routes at startup. Inside a container,
# 127.0.0.1 is unreachable from other containers, e.g. the proxy. On
# 0.0.0.0, containers on the same network reach it, and the host's network
# only if the port is published.
ENV GIN_MODE=release \
    INBOK_HTTP_ADDR=0.0.0.0:8080
EXPOSE 8080
# The uid of distroless's nonroot user, as a number so a runtime can check it
# is not root.
USER 65532:65532
# Exec form: there is no shell. inbok health gives up after 3 seconds,
# inside this 5-second timeout.
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --start-interval=2s --retries=3 \
    CMD ["/usr/local/bin/inbok", "health"]
ENTRYPOINT ["/usr/local/bin/inbok"]
CMD ["serve"]
