# syntax=docker/dockerfile:1
# Preserve the qualified CLIs, bubblewrap and filesystem layout; replace only
# the runtime binary. The inherited image remains non-root and capability-free.
ARG RUNTIME_BASE
FROM ${RUNTIME_BASE}
COPY --chmod=0555 agentserver-k8s-runtime /usr/local/bin/agentserver-k8s-runtime
