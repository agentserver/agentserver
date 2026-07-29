FROM scratch

COPY conformance.test /opt/agentserver/conformance.test
COPY codex /opt/agentserver/runtime/bin/codex
COPY bwrap /opt/agentserver/runtime/codex-resources/bwrap
COPY conformance.test /opt/agentserver/poison/bwrap

ENTRYPOINT ["/opt/agentserver/conformance.test"]
