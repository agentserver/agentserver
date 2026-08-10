FROM docker.io/library/postgres@sha256:ef257d85f76e48da1c64832459b59fcaba1a4dac97bf5d7450c77753542eee94 AS certificates

FROM aliyun-sin-hub.byted.org/agentserver/tae-sandbox@sha256:e4255f02c1feceb168848fc6b7ea934cdc3f944ebc8dda51d2b77d00fbf28f6f

ARG SOURCE_REVISION
LABEL org.opencontainers.image.title="agentserver v2 managed sandbox"
LABEL org.opencontainers.image.description="Digest-pinned Debian TAE sandbox with closed-world Lark runtime"
LABEL org.opencontainers.image.revision="${SOURCE_REVISION}"
LABEL org.opencontainers.image.source="https://github.com/agentserver/agentserver"

ADD --chown=0:0 rootfs.tar /
COPY --from=certificates --chown=0:0 --chmod=0444 /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

USER 65534:65534
WORKDIR /workspace
CMD []
STOPSIGNAL SIGTERM
