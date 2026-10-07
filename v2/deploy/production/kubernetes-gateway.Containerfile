# syntax=docker/dockerfile:1
FROM debian:trixie-slim AS certificates
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates
FROM scratch
COPY --from=certificates /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --chmod=0555 sandbox-gateway-k8s /usr/local/bin/sandbox-gateway-k8s
USER 65534:65534
ENTRYPOINT ["/usr/local/bin/sandbox-gateway-k8s"]
STOPSIGNAL SIGTERM
