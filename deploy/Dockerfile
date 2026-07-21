# Runtime image for the patched OSS Teleport (Google SSO / OIDC enabled).
#
# This is a small runtime-only image: it expects the teleport (and tctl)
# binaries to already be built and present under build/ in the build context.
# The GitHub Actions workflow (.github/workflows/build-teleport-image.yml)
# builds those binaries inside the Teleport buildbox, then builds this image.
#
# Build locally (after `make full`):
#   docker build -f deploy/Dockerfile -t teleport-oidc:local .
FROM debian:12-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates dumb-init \
    && rm -rf /var/lib/apt/lists/*

# Binaries produced by `make full` (webassets embedded in teleport).
COPY build/teleport /usr/local/bin/teleport
COPY build/tctl /usr/local/bin/tctl

RUN mkdir -p /etc/teleport /var/lib/teleport

# Disable the managed-updates client inside a container image.
ENV TELEPORT_TOOLS_VERSION=off

# Web/proxy (multiplexed) and auth ports.
EXPOSE 3080 3025

# Graceful shutdown, see https://goteleport.com/docs/reference/signals/
STOPSIGNAL SIGQUIT

ENTRYPOINT ["/usr/bin/dumb-init", "/usr/local/bin/teleport", "start", "-c", "/etc/teleport/teleport.yaml"]
