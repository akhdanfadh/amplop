# syntax=docker/dockerfile:1

# The Google emulator for amplop's tests and its development and trial
# stacks: vercel-labs' emulate (https://github.com/vercel-labs/emulate),
# a stateful fake of Google OAuth and the Gmail API. emulate publishes
# only an npm package, so this builds the image. compose.yaml runs it, and
# backend/internal/googleemulator starts it for tests.

# Base images, pinned by version and by digest, as in the repository's
# Dockerfile, which says how to look up a digest. Keep pnpm's version
# equal to devEngines.packageManager in package.json and to the web's.
ARG PNPM_IMAGE=ghcr.io/pnpm/pnpm:12.8.1@sha256:44cfd11d75b901dc7a83a16812d9134e4bf9d82f6b0eb28af6d666ebbf0fb4fb
ARG NODE_IMAGE=node:24.22.2-trixie-slim@sha256:54f84b4cf8432b21aaffd5d3ce8d41e12cea0d10d33c09d2f3c9879d49d6b446

# emulate as pnpm-lock.yaml pins it, with amplop's patch applied (see
# pnpm-workspace.yaml). emulate runs no install scripts, so this needs no
# Node.
FROM ${PNPM_IMAGE} AS install
WORKDIR /emulator
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY patches/ patches/
# No cache mount, unlike the repository's Dockerfile: Testcontainers builds
# with Docker's classic builder, which has none, and four packages download
# quickly.
RUN pnpm install --frozen-lockfile --prod

FROM ${NODE_IMAGE}
WORKDIR /emulator
# pnpm's links inside node_modules are relative, so they survive the copy.
COPY --from=install /emulator/node_modules/ node_modules/
COPY emulate.config.mjs ./

USER node
EXPOSE 4000
# 0.0.0.0 so a published port reaches it. EMULATE_BASE_URL must be the
# address clients use, e.g. http://localhost:4000: it is the ID token's
# issuer and the base of the addresses the emulator hands out.
ENTRYPOINT ["node", "node_modules/emulate/dist/index.js", "start", "--service", "google", "--host", "0.0.0.0", "--port", "4000", "--config", "emulate.config.mjs"]
HEALTHCHECK --interval=2s --timeout=3s --retries=15 \
  CMD ["node", "-e", "fetch('http://127.0.0.1:4000/.well-known/openid-configuration').then((r) => process.exit(r.ok ? 0 : 1), () => process.exit(1))"]
