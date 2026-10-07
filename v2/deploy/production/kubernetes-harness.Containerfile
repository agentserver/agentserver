# syntax=docker/dockerfile:1
# Keep the qualified OS/final-exec executable. Replace both the stock Codex
# bundle and its runtime metadata, plus this release's application binaries.
ARG HARNESS_BASE
FROM ${HARNESS_BASE}
ARG SOURCE_REVISION
LABEL org.opencontainers.image.revision="${SOURCE_REVISION}"
COPY --chmod=0555 bin/ /usr/local/bin/
COPY runtime/ /opt/agentserver/runtime/
USER 65530:65530
WORKDIR /
STOPSIGNAL SIGTERM
