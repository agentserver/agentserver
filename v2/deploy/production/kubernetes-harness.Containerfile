# syntax=docker/dockerfile:1
# Reuse the installed stock Codex bundle and unchanged final-exec executable.
# The deployment retains that base's runtime metadata; all application-facing
# pool, worker and init binaries are rebuilt from this release's source.
ARG HARNESS_BASE
FROM ${HARNESS_BASE}
ARG SOURCE_REVISION
LABEL org.opencontainers.image.revision="${SOURCE_REVISION}"
COPY --chmod=0555 bin/ /usr/local/bin/
USER 65530:65530
WORKDIR /
STOPSIGNAL SIGTERM
