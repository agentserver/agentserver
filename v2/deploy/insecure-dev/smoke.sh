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

database_scalar() {
    container exec \
        --env PGPASSWORD=development \
        "${name}" \
        psql -h 127.0.0.1 -U agentserver -d agentserver -At \
        -c "$1"
}

validated_count() {
    value=$(database_scalar "$1")
    value=$(printf '%s' "${value}" | tr -d '[:space:]')
    case "${value}" in ''|*[!0-9]*) printf '%s\n' "smoke.sh: invalid ${2} count" >&2; exit 1 ;; esac
    printf '%s' "${value}"
}

before_checkpoint_count=$(validated_count 'SELECT count(*) FROM agentserver_v2.checkpoints' 'initial checkpoint')
before_approval_count=$(validated_count 'SELECT count(*) FROM agentserver_v2.approvals' 'initial approval')
before_operation_count=$(validated_count 'SELECT count(*) FROM agentserver_v2.execution_operations' 'initial execution operation')
before_dispatched_operation_count=$(validated_count 'SELECT count(*) FROM agentserver_v2.execution_operations WHERE dispatched_at IS NOT NULL' 'initial dispatched execution operation')

printf '%s\n' "smoke.sh: starting five TLS 1.3 AG-UI requests through the published host port"
smoke_output=$(
    cd "${v2_root}"
    env GOCACHE="${TMPDIR:-/tmp}/agentserver-v2-smoke-go-cache" \
        go run ./cmd/agentserver-dev-smoke \
        --origin="https://127.0.0.1:${browser_port}" \
        --ca-file="${smoke_root}/ca.pem" \
        --bearer-file="${smoke_root}/browser-bearer.token"
)
printf '%s\n' "${smoke_output}"

smoke_result() {
    printf '%s\n' "${smoke_output}" | awk -v scenario="$1" \
        '$1 == "agentserver-dev-smoke-result" && $2 == scenario { print $3 " " $4 }'
}

validate_result_ids() {
    result=$1
    scenario=$2
    run_id=${result%% *}
    approval_id=${result#* }
    uuid_pattern='^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
    if [ "${run_id}" = "${approval_id}" ] || \
        ! printf '%s\n' "${run_id}" | grep -Eq "${uuid_pattern}" || \
        ! printf '%s\n' "${approval_id}" | grep -Eq "${uuid_pattern}"
    then
        printf '%s\n' "smoke.sh: ${scenario} result did not contain canonical run and approval IDs" >&2
        exit 1
    fi
}

denied_result=$(smoke_result denied)
expired_result=$(smoke_result expired)
pending_cancel_result=$(smoke_result pending-cancel)
validate_result_ids "${denied_result}" denied
validate_result_ids "${expired_result}" expired
validate_result_ids "${pending_cancel_result}" pending-cancel

checkpoint_count=$(validated_count 'SELECT count(*) FROM agentserver_v2.checkpoints' 'final checkpoint')
approval_count=$(validated_count 'SELECT count(*) FROM agentserver_v2.approvals' 'final approval')
operation_count=$(validated_count 'SELECT count(*) FROM agentserver_v2.execution_operations' 'final execution operation')
dispatched_operation_count=$(validated_count 'SELECT count(*) FROM agentserver_v2.execution_operations WHERE dispatched_at IS NOT NULL' 'final dispatched execution operation')

expected_checkpoint_count=$((before_checkpoint_count + 3))
[ "${checkpoint_count}" -eq "${expected_checkpoint_count}" ] || {
    printf '%s\n' "smoke.sh: expected normal, denied, and expired runs to commit three checkpoints; count changed ${before_checkpoint_count} -> ${checkpoint_count}" >&2
    exit 1
}
expected_approval_count=$((before_approval_count + 5))
[ "${approval_count}" -eq "${expected_approval_count}" ] || {
    printf '%s\n' "smoke.sh: expected five canonical approvals; count changed ${before_approval_count} -> ${approval_count}" >&2
    exit 1
}
expected_operation_count=$((before_operation_count + 4))
[ "${operation_count}" -eq "${expected_operation_count}" ] || {
    printf '%s\n' "smoke.sh: expected two approved shell executions to freeze four operation-plan rows; count changed ${before_operation_count} -> ${operation_count}" >&2
    exit 1
}
expected_dispatched_operation_count=$((before_dispatched_operation_count + 2))
[ "${dispatched_operation_count}" -eq "${expected_dispatched_operation_count}" ] || {
    printf '%s\n' "smoke.sh: expected exactly two process-start operations to dispatch; count changed ${before_dispatched_operation_count} -> ${dispatched_operation_count}" >&2
    exit 1
}

assert_gate_state() {
    result=$1
    expected_approval=$2
    expected_run=$3
    expected_checkpoints=$4
    run_id=${result%% *}
    approval_id=${result#* }
    gate_state=$(database_scalar "SELECT a.run_id::text || ':' || a.status || ':' || e.status || ':' || CASE WHEN e.dispatched_at IS NULL THEN 'undispatched' ELSE 'dispatched' END || ':' || count(o.id)::text FROM agentserver_v2.approvals AS a JOIN agentserver_v2.executions AS e ON e.id = a.execution_id LEFT JOIN agentserver_v2.execution_operations AS o ON o.execution_id = e.id WHERE a.id = '${approval_id}'::uuid GROUP BY a.run_id, a.status, e.status, e.dispatched_at")
    gate_state=$(printf '%s' "${gate_state}" | tr -d '[:space:]')
    expected_gate_state="${run_id}:${expected_approval}:${expected_approval}:undispatched:0"
    [ "${gate_state}" = "${expected_gate_state}" ] || {
        printf '%s\n' "smoke.sh: ${expected_approval} gate state is ${gate_state:-missing}, want ${expected_gate_state}" >&2
        exit 1
    }
    run_state=$(database_scalar "SELECT r.status || ':' || count(c.id)::text FROM agentserver_v2.runs AS r LEFT JOIN agentserver_v2.checkpoints AS c ON c.run_id = r.id WHERE r.id = '${run_id}'::uuid GROUP BY r.status")
    run_state=$(printf '%s' "${run_state}" | tr -d '[:space:]')
    expected_run_state="${expected_run}:${expected_checkpoints}"
    [ "${run_state}" = "${expected_run_state}" ] || {
        printf '%s\n' "smoke.sh: ${expected_approval} run state is ${run_state:-missing}, want ${expected_run_state}" >&2
        exit 1
    }
}

assert_gate_state "${denied_result}" denied completed 1
assert_gate_state "${expired_result}" expired completed 1
assert_gate_state "${pending_cancel_result}" cancelled cancelled 0

printf '%s\n' "smoke.sh: passed AG-UI -> Core -> harness-worker -> stock app-server -> MCP -> agentx -> stock exec-server"
printf '%s\n' "smoke.sh: deny, expiry, and pending-cancel produced zero agentx dispatch and zero execution operations; checkpoints=${checkpoint_count} approvals=${approval_count} operations=${operation_count} dispatched_operations=${dispatched_operation_count}"
