# syntax=docker/dockerfile:1
# Preserve the qualified CLIs, bubblewrap and filesystem layout. Refresh the
# runtime, managed instructions and JSON processor; final execution is non-root.
ARG RUNTIME_BASE
FROM ${RUNTIME_BASE}
USER 0:0
RUN apt-get update && apt-get install -y --no-install-recommends jq git && apt-get clean
COPY --chmod=0555 agentserver-k8s-runtime /usr/local/bin/agentserver-k8s-runtime
COPY --chown=0:0 packs/ /opt/agentserver/packs/
USER 10000:10000
