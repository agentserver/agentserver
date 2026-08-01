#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
v2_root=$(CDPATH= cd -- "${script_dir}/../.." && pwd -P)
name=agentserver-v2-dev
browser_port=17444

usage() {
    printf '%s\n' 'usage: smoke.sh [--name=container-name] [--browser-port=17444]'
}

for argument in "$@"; do
    case "${argument}" in
        --name=*) name=${argument#--name=} ;;
        --browser-port=*) browser_port=${argument#--browser-port=} ;;
        --help|-h) usage; exit 0 ;;
        *) usage >&2; exit 2 ;;
    esac
done
case "${browser_port}" in ''|*[!0-9]*) usage >&2; exit 2 ;; esac
[ "${browser_port}" -ge 1 ] && [ "${browser_port}" -le 65535 ] || { usage >&2; exit 2; }
[ -n "${name}" ] || { usage >&2; exit 2; }

container exec "${name}" test -f /run/agentserver-v2/ready

smoke_root=$(mktemp -d "${TMPDIR:-/tmp}/agentserver-v2-smoke.XXXXXX")
cleanup() {
    rm -rf -- "${smoke_root}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

container exec "${name}" /bin/cat /var/lib/agentserver/stack/pki/ca.pem >"${smoke_root}/ca.pem"
container exec "${name}" /bin/cat /var/lib/agentserver/stack/secrets/browser-bearer.token >"${smoke_root}/browser-bearer.token"
[ -s "${smoke_root}/browser-bearer.token" ] || { printf '%s\n' 'smoke.sh: browser bearer is empty' >&2; exit 1; }
chmod 0600 "${smoke_root}/ca.pem" "${smoke_root}/browser-bearer.token"

before_checkpoint_count=$(container exec \
    --env PGPASSWORD=development \
    "${name}" \
    psql -h 127.0.0.1 -U agentserver -d agentserver -At \
    -c 'SELECT count(*) FROM agentserver_v2.checkpoints')
before_checkpoint_count=$(printf '%s' "${before_checkpoint_count}" | tr -d '[:space:]')
case "${before_checkpoint_count}" in ''|*[!0-9]*) printf '%s\n' 'smoke.sh: invalid initial checkpoint count' >&2; exit 1 ;; esac

printf '%s\n' "smoke.sh: starting a TLS 1.3 AG-UI request through the published host port"
(
    cd "${v2_root}"
    env GOCACHE="${TMPDIR:-/tmp}/agentserver-v2-smoke-go-cache" \
        go run ./cmd/agentserver-dev-smoke \
        --origin="https://127.0.0.1:${browser_port}" \
        --ca-file="${smoke_root}/ca.pem" \
        --bearer-file="${smoke_root}/browser-bearer.token"
)

checkpoint_count=$(container exec \
    --env PGPASSWORD=development \
    "${name}" \
    psql -h 127.0.0.1 -U agentserver -d agentserver -At \
    -c 'SELECT count(*) FROM agentserver_v2.checkpoints')
checkpoint_count=$(printf '%s' "${checkpoint_count}" | tr -d '[:space:]')
case "${checkpoint_count}" in ''|*[!0-9]*) printf '%s\n' 'smoke.sh: invalid final checkpoint count' >&2; exit 1 ;; esac
expected_checkpoint_count=$((before_checkpoint_count + 1))
[ "${checkpoint_count}" -eq "${expected_checkpoint_count}" ] || {
    printf '%s\n' "smoke.sh: expected exactly one normal-run checkpoint, count changed ${before_checkpoint_count} -> ${checkpoint_count}" >&2
    exit 1
}

latest_run_state=$(container exec \
    --env PGPASSWORD=development \
    "${name}" \
    psql -h 127.0.0.1 -U agentserver -d agentserver -At \
    -c "SELECT r.status || ':' || count(c.id)::text FROM agentserver_v2.runs AS r LEFT JOIN agentserver_v2.checkpoints AS c ON c.run_id = r.id WHERE r.id = (SELECT id FROM agentserver_v2.runs ORDER BY created_at DESC, id DESC LIMIT 1) GROUP BY r.status")
latest_run_state=$(printf '%s' "${latest_run_state}" | tr -d '[:space:]')
[ "${latest_run_state}" = 'cancelled:0' ] || {
    printf '%s\n' "smoke.sh: latest cancellation run/checkpoint state is ${latest_run_state:-missing}, want cancelled:0" >&2
    exit 1
}

printf '%s\n' "smoke.sh: passed AG-UI -> Core -> harness-worker -> stock app-server -> MCP -> agentx -> stock exec-server"
printf '%s\n' "smoke.sh: one normal checkpoint committed; explicit cancellation committed no checkpoint (total ${checkpoint_count})"
