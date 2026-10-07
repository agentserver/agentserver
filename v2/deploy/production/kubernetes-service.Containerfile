# syntax=docker/dockerfile:1
FROM debian:trixie-slim AS certificates
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates
FROM scratch
ARG SOURCE_REVISION
LABEL org.opencontainers.image.revision="${SOURCE_REVISION}"
LABEL org.opencontainers.image.source="https://github.com/agentserver/agentserver"
COPY --from=certificates /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --chmod=0555 bin/ /usr/local/bin/
USER 65534:65534
WORKDIR /
STOPSIGNAL SIGTERM
