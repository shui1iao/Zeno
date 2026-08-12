# syntax=docker/dockerfile:1
# Builder versions match the CI toolchains exactly, so the shipped binary and
# assets are produced by the runtimes that passed the full test suite. Digest
# pins keep rebuild inputs immutable; version upgrades update CI and these two
# stages together. Published images include provenance and SBOM attestations.

FROM --platform=$BUILDPLATFORM node:26.7.0-bookworm-slim@sha256:cd565714d4da3e84bfd341e31448f81d47c6362198f152345297c9c1154e6341 AS web-builder
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
ARG VERSION=dev
ENV VITE_BUILD_ID=${VERSION}
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26.5-bookworm@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
COPY --from=web-builder /src/web/dist ./web/dist
ARG VERSION=dev
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT
RUN if [ "${TARGETARCH}/${TARGETVARIANT}" = "arm/v6" ]; then export GOARM=6; fi \
  && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags "-s -w" -o /out/zeno-controller ./cmd/controller

FROM debian:13.6-slim@sha256:020c0d20b9880058cbe785a9db107156c3c75c2ac944a6aa7ab59f2add76a7bd
ARG VERSION=dev
ARG REVISION=unknown
ARG ZENO_UID=10001
ARG ZENO_GID=10001
LABEL org.opencontainers.image.title="Zeno" \
  org.opencontainers.image.description="Lightweight self-hosted server monitor" \
  org.opencontainers.image.source="https://github.com/shui1iao/Zeno" \
  org.opencontainers.image.url="https://github.com/shui1iao/Zeno" \
  org.opencontainers.image.licenses="MIT" \
  org.opencontainers.image.version="${VERSION}" \
  org.opencontainers.image.revision="${REVISION}"
RUN apt-get update \
  && apt-get install -y --no-install-recommends --only-upgrade libcap2 \
  && apt-get install -y --no-install-recommends ca-certificates curl iputils-ping tzdata \
  && rm -rf /var/lib/apt/lists/* \
  && groupadd --system --gid "${ZENO_GID}" zeno \
  && useradd --system --uid "${ZENO_UID}" --gid zeno --home-dir /opt/zeno --shell /usr/sbin/nologin zeno \
  && mkdir -p /opt/zeno /data \
  && chown -R zeno:zeno /opt/zeno /data
WORKDIR /opt/zeno
COPY --from=go-builder /out/zeno-controller /usr/local/bin/zeno-controller
COPY --from=web-builder /src/web/dist /opt/zeno/web
COPY LICENSE THIRD_PARTY_NOTICES.txt /usr/share/doc/zeno/
RUN chown -R zeno:zeno /opt/zeno/web /usr/local/bin/zeno-controller
ENV TZ=Asia/Shanghai
EXPOSE 18980
USER zeno:zeno
ENTRYPOINT ["/usr/local/bin/zeno-controller"]
CMD ["-addr", "0.0.0.0:18980", "-web-dir", "/opt/zeno/web", "-db", "/data/zeno.db", "-admin-token-file", "/run/secrets/zeno_admin_token", "-agent-token-file", "/run/secrets/zeno_agent_token", "-notification-authority-key-file", "/run/secrets/zeno_notification_authority"]
