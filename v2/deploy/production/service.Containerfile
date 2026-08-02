FROM docker.io/library/postgres@sha256:9b9fb55f7e3b2149854def33c728b781dc44d1c5e86492ad62912a527ae234b3 AS certificates

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
