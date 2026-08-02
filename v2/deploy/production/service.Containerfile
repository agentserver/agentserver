FROM docker.io/library/postgres@sha256:ef257d85f76e48da1c64832459b59fcaba1a4dac97bf5d7450c77753542eee94 AS certificates

FROM scratch

ARG SOURCE_REVISION
LABEL org.opencontainers.image.title="agentserver v2 production services"
LABEL org.opencontainers.image.description="Closed-world service binaries for agentserver v2"
LABEL org.opencontainers.image.revision="${SOURCE_REVISION}"
LABEL org.opencontainers.image.source="https://github.com/agentserver/agentserver"

ADD --chown=0:0 rootfs.tar /
COPY --from=certificates --chown=0:0 --chmod=0444 /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

USER 65534:65534
WORKDIR /
STOPSIGNAL SIGTERM
