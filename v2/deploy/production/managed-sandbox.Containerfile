FROM docker.io/library/postgres@sha256:ef257d85f76e48da1c64832459b59fcaba1a4dac97bf5d7450c77753542eee94 AS certificates

# Agentserver-owned, single-layer Debian runtime base. This is intentionally
# not faas/bytedance.sandbox.terminal_faas: the managed overlay below owns the
# FaaS keeper while the session create request selects it as the command and
# TAE injects SandboxD at runtime.
FROM aliyun-sin-hub.byted.org/agentserver/tae-sandbox@sha256:e4255f02c1feceb168848fc6b7ea934cdc3f944ebc8dda51d2b77d00fbf28f6f

ARG SOURCE_REVISION
LABEL org.opencontainers.image.title="agentserver v2 managed sandbox"
LABEL org.opencontainers.image.description="Agentserver-owned minimal Debian TAE runtime with closed-world Lark tooling"
LABEL org.opencontainers.image.revision="${SOURCE_REVISION}"
LABEL org.opencontainers.image.source="https://github.com/agentserver/agentserver"

ADD --chown=0:0 rootfs.tar /
COPY --from=certificates --chown=0:0 --chmod=0444 /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

USER 0:0
WORKDIR /workspace
CMD ["/usr/local/bin/agentserver-tae-runtime"]
STOPSIGNAL SIGTERM
