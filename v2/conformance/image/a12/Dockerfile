FROM scratch

COPY conformance.test /opt/agentserver/conformance.test
COPY codex /opt/agentserver/runtime/bin/codex
COPY mount-anchor /tmp/.mount-anchor
COPY mount-anchor /run/agentserver/.mount-anchor

ENTRYPOINT ["/opt/agentserver/conformance.test"]
