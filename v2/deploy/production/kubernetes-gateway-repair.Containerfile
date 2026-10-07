# Restore the standard TCP health helper without changing gateway code.
ARG SERVICE_BASE
ARG GATEWAY_BASE
FROM ${SERVICE_BASE} AS helper
FROM ${GATEWAY_BASE}
COPY --from=helper --chmod=0555 /usr/local/bin/agentserver-probe /usr/local/bin/agentserver-probe
