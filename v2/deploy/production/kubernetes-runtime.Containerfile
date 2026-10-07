# syntax=docker/dockerfile:1
# Build context is prepared by build-kubernetes-images.sh. This image does not
# inherit the TAE keeper or depend on TAE-injected sandboxd.
FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates \
    && apt-get clean
COPY --chmod=0555 agentserver-k8s-runtime bwrap bkectl lark-cli /usr/local/bin/
COPY --chown=0:0 packs/ /opt/agentserver/packs/
RUN mkdir -p /workspace && chown 10000:10000 /workspace
USER 10000:10000
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/agentserver-k8s-runtime"]
STOPSIGNAL SIGTERM
