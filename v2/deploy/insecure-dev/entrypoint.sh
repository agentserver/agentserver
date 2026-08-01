#!/bin/sh
set -eu

state_root=/var/lib/agentserver
stack_root=${state_root}/stack
stack_config=${state_root}/stack-config.json
ready_root=/run/agentserver-v2
ready_file=${ready_root}/ready
worker_uid=65531
worker_gid=65531

children=""
child_names=""
postgres_pid=""

log() {
    printf '%s\n' "agentserver-v2-dev: $*"
}

fail() {
    printf '%s\n' "agentserver-v2-dev: $*" >&2
    exit 1
}

record_child() {
    children="${children} $1"
    child_names="${child_names} $2:$1"
}

cleanup() {
    status=$?
    trap - EXIT INT TERM
    rm -f "${ready_file}"
    for pid in ${children}; do
        signal=TERM
        if [ -n "${postgres_pid}" ] && [ "${pid}" = "${postgres_pid}" ]; then
            # Fast shutdown rolls back active work and checkpoints cleanly;
            # smart shutdown can outlive a container stop grace period while
            # long-poll clients remain connected.
            signal=INT
        fi
        kill -"${signal}" "${pid}" 2>/dev/null || true
    done
    remaining=${children}
    attempts=0
    while [ -n "${remaining}" ] && [ "${attempts}" -lt 100 ]; do
        next=""
        for pid in ${remaining}; do
            if kill -0 "${pid}" 2>/dev/null; then
                next="${next} ${pid}"
            fi
        done
        remaining=${next}
        attempts=$((attempts + 1))
        if [ -n "${remaining}" ]; then
            sleep 0.1
        fi
    done
    for pid in ${remaining}; do
        kill -KILL "${pid}" 2>/dev/null || true
    done
    for pid in ${children}; do
        wait "${pid}" 2>/dev/null || true
    done
    exit "${status}"
}

trap cleanup EXIT
trap 'exit 0' INT TERM

require_root() {
    [ "$(id -u)" -eq 0 ] || fail "the supervisor must start as container root"
}

prepare_state_root() {
    mkdir -p "${state_root}" "${ready_root}"
    chmod 0711 "${state_root}"
    chmod 0700 "${ready_root}"
    rm -f "${ready_file}"
}

start_postgres() {
    log "starting PostgreSQL on loopback"
    /usr/local/bin/docker-entrypoint.sh postgres -c listen_addresses=127.0.0.1 &
    postgres_pid=$!
    record_child "${postgres_pid}" postgres

    attempts=0
    until pg_isready -q -h 127.0.0.1 -U "${POSTGRES_USER}" -d "${POSTGRES_DB}"; do
        kill -0 "${postgres_pid}" 2>/dev/null || fail "PostgreSQL exited during initialization"
        attempts=$((attempts + 1))
        [ "${attempts}" -lt 120 ] || fail "PostgreSQL was not ready after 60 seconds"
        sleep 0.5
    done
}

write_stack_config() {
    if [ -e "${stack_config}" ]; then
        [ -f "${stack_config}" ] || fail "${stack_config} is not a regular file"
        chmod 0600 "${stack_config}"
        return
    fi
    umask 077
    temp_config=${stack_config}.tmp
    rm -f "${temp_config}"
    cat >"${temp_config}" <<'EOF'
{
  "version": 1,
  "databaseUrl": "postgres://agentserver:development@127.0.0.1:5432/agentserver?sslmode=disable",
  "authority": {
    "workspaceId": "40000000-0000-4000-8000-000000000004",
    "sessionId": "50000000-0000-4000-8000-000000000005",
    "actorId": "10000000-0000-4000-8000-000000000001",
    "executorId": "20000000-0000-4000-8000-000000000002",
    "environmentId": "60000000-0000-4000-8000-000000000006",
    "agentxVersion": "0.1.0-dev",
    "workspaceRoot": "/workspace",
    "displayName": "Mounted development workspace",
    "description": "single-container insecure development executor",
    "defaultCwd": "."
  },
  "runtime": {
    "manifestFile": "/opt/agentserver/stock-runtime/runtime-manifest.json",
    "bundleRoot": "/opt/agentserver/stock-runtime/bundle",
    "agentxBinary": "/usr/local/bin/agentx",
    "harnessWorkerBinary": "/usr/local/bin/harness-worker",
    "harnessFinalExecBinary": "/usr/local/bin/harness-final-exec"
  },
  "network": {
    "coreListenAddress": "127.0.0.1:17443",
    "browserGatewayListenAddress": "127.0.0.1:17444",
    "executorGatewayListenAddress": "127.0.0.1:17445",
    "harnessPoolListenAddress": "127.0.0.1:17446",
    "hydraIntrospectionUrl": "http://127.0.0.1:17447/oauth2/introspect",
    "llmproxyEndpoint": "https://127.0.0.1:17448/v1"
  },
  "model": {"name": "gpt-5", "provider": "llmproxy"},
  "policy": {
    "version": "dev-v1",
    "allowedTools": ["list_environments", "shell", "read_file"]
  },
  "harness": {"maxConcurrentAttempts": 2, "maxRunDuration": "30m", "maxApprovalTtl": "10s"},
  "identities": {
    "workerUid": 65531,
    "workerGid": 65531,
    "appUid": 65532,
    "appGid": 65532
  }
}
EOF
    chmod 0600 "${temp_config}"
    mv "${temp_config}" "${stack_config}"
}

prepare_stack() {
    if [ ! -e "${stack_root}" ]; then
        log "creating a new insecure development authority"
        write_stack_config
        /usr/local/bin/agentserver-dev prepare --insecure-dev \
            --config="${stack_config}" \
            --output-dir="${stack_root}"
    fi
    [ -d "${stack_root}" ] || fail "prepared stack path is not a directory"
    # fixtures validates and opens the private bundle before the pool grants
    # execute-only traversal to the fixed worker/app identities. A persisted
    # stack is reset to this pre-launch mode on every clean container start.
    chmod 0700 "${stack_root}"
}

install_system_ca() {
    ca_file=${stack_root}/pki/ca.pem
    [ -f "${ca_file}" ] || fail "prepared development CA is missing"
    [ ! -e /etc/ssl/certs/agentserver-v2-insecure-dev.pem ] || fail "development CA was already installed"
    cp "${ca_file}" /etc/ssl/certs/agentserver-v2-insecure-dev.pem
    chmod 0444 /etc/ssl/certs/agentserver-v2-insecure-dev.pem
    printf '\n' >>/etc/ssl/certs/ca-certificates.crt
    cat "${ca_file}" >>/etc/ssl/certs/ca-certificates.crt
}

grant_worker_inputs() {
    chmod 0711 \
        "${stack_root}" \
        "${stack_root}/config" \
        "${stack_root}/pki" \
        "${stack_root}/state" \
        "${stack_root}/state/harness-runtime"

    for path in \
        "${stack_root}/config/harness-worker.json" \
        "${stack_root}/config/run-manifest-keyring.json" \
        "${stack_root}/pki/ca.pem" \
        "${stack_root}/pki/harness-worker.crt" \
        "${stack_root}/pki/harness-worker.key"
    do
        chown "${worker_uid}:${worker_gid}" "${path}"
        chmod 0400 "${path}"
    done
}

run_with_environment() {
    environment_file=$1
    shift
    env -i \
        PATH=/usr/local/bin:/usr/bin:/bin \
        HOME=/root \
        LANG=C \
        LC_ALL=C \
        /bin/sh -c '
            environment_file=$1
            shift
            . "${environment_file}"
            exec "$@"
        ' agentserver-service "${environment_file}" "$@"
}

start_with_environment() {
    name=$1
    environment_file=$2
    shift 2
    run_with_environment "${environment_file}" "$@" &
    pid=$!
    record_child "${pid}" "${name}"
}

start_browser_gateway() {
    environment_file=${stack_root}/env/browser-gateway.env
    env -i \
        PATH=/usr/local/bin:/usr/bin:/bin \
        HOME=/root \
        LANG=C \
        LC_ALL=C \
        /bin/sh -c '
            . "$1"
            # Apple container publishes to the container NIC, not its
            # loopback device. Only the authenticated browser boundary gets
            # this deployment-level bind override; every internal endpoint
            # remains loopback-only.
            export AGENTSERVER_V2_BROWSER_GATEWAY_LISTEN_ADDR=0.0.0.0:17444
            exec /usr/local/bin/browser-gateway serve
        ' browser-gateway "${environment_file}" &
    pid=$!
    record_child "${pid}" browser-gateway
}

start_clean() {
    name=$1
    shift
    env -i PATH=/usr/local/bin:/usr/bin:/bin HOME=/root LANG=C LC_ALL=C "$@" &
    pid=$!
    record_child "${pid}" "${name}"
}

wait_for_port() {
    name=$1
    port=$2
    attempts=0
    until nc -z 127.0.0.1 "${port}" >/dev/null 2>&1; do
        attempts=$((attempts + 1))
        [ "${attempts}" -lt 100 ] || fail "${name} did not listen on port ${port}"
        sleep 0.1
    done
}

start_agentserver() {
    log "bootstrapping Core authority"
    run_with_environment \
        "${stack_root}/env/agentserver-core.env" \
        /usr/local/bin/agentserver-core bootstrap --insecure-dev \
        --config="${stack_root}/config/core-bootstrap.json"

    start_clean fixtures \
        /usr/local/bin/agentserver-dev fixtures --insecure-dev --bundle="${stack_root}"
    wait_for_port hydra-fixture 17447
    wait_for_port llmproxy-fixture 17448
    grant_worker_inputs

    start_with_environment core "${stack_root}/env/agentserver-core.env" \
        /usr/local/bin/agentserver-core serve
    wait_for_port core 17443

    start_browser_gateway
    start_with_environment executor-gateway "${stack_root}/env/executor-gateway.env" \
        /usr/local/bin/executor-gateway serve --insecure-dev
    start_with_environment harness-pool "${stack_root}/env/harness-pool.env" \
        /usr/local/bin/harness-pool serve --insecure-dev

    wait_for_port browser-gateway 17444
    wait_for_port executor-gateway 17445
    wait_for_port harness-pool 17446

    start_clean agentx \
        /usr/local/bin/agentx connect --insecure-dev \
        --gateway-url=wss://127.0.0.1:17445/internal/v2/agentx/connect \
        --gateway-ca-file="${stack_root}/pki/ca.pem" \
        --executor-id=20000000-0000-4000-8000-000000000002 \
        --environment-id=60000000-0000-4000-8000-000000000006 \
        --runtime-manifest=/opt/agentserver/stock-runtime/runtime-manifest.json \
        --runtime-root=/opt/agentserver/stock-runtime/bundle \
        --runtime-dir="${stack_root}/state/agentx-runtime" \
        --workspace-root=/workspace

    sleep 1
    for pid in ${children}; do
        kill -0 "${pid}" 2>/dev/null || fail "a development component exited during startup"
    done
    wget -q -O /dev/null https://127.0.0.1:17444/readyz || fail "browser-gateway readiness check failed"
    printf '%s\n' ready >"${ready_file}"
    chmod 0400 "${ready_file}"
    log "ready; browser AG-UI is available on container port 17444"
}

monitor_children() {
    while :; do
        for entry in ${child_names}; do
            name=${entry%%:*}
            pid=${entry##*:}
            if ! kill -0 "${pid}" 2>/dev/null; then
                wait "${pid}" || status=$?
                fail "${name} exited unexpectedly with status ${status:-0}"
            fi
        done
        sleep 1
    done
}

require_root
prepare_state_root
start_postgres
prepare_stack
install_system_ca
start_agentserver
monitor_children
