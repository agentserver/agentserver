FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends apparmor ca-certificates \
    && apt-get clean
COPY --chmod=0555 install-profiles.sh /usr/local/bin/install-agentserver-profiles
ENTRYPOINT ["/usr/local/bin/install-agentserver-profiles"]
