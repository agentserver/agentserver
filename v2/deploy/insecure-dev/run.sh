#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
v2_root=$(CDPATH= cd -- "${script_dir}/../.." && pwd -P)
workspace=$(CDPATH= cd -- "${v2_root}/.." && pwd -P)
state_volume=""
image=agentserver-v2-insecure-dev:0.146.0
name=agentserver-v2-dev
browser_port=17444
cpus=4
memory=4G

usage() {
    cat <<'EOF'
usage: run.sh [--image=name:tag] [--name=container-name]
              [--workspace=/absolute/directory] [--state-volume=volume-name]
              [--browser-port=17444] [--cpus=4] [--memory=4G]
EOF
}

for argument in "$@"; do
    case "${argument}" in
        --image=*) image=${argument#--image=} ;;
        --name=*) name=${argument#--name=} ;;
        --workspace=*) workspace=${argument#--workspace=} ;;
        --state-volume=*) state_volume=${argument#--state-volume=} ;;
        --browser-port=*) browser_port=${argument#--browser-port=} ;;
        --cpus=*) cpus=${argument#--cpus=} ;;
        --memory=*) memory=${argument#--memory=} ;;
        --help|-h) usage; exit 0 ;;
        *) usage >&2; exit 2 ;;
    esac
done

case "${workspace}" in /*) ;; *) usage >&2; exit 2 ;; esac
[ -d "${workspace}" ] || { printf '%s\n' "run.sh: workspace is not a directory" >&2; exit 1; }
workspace=$(CDPATH= cd -- "${workspace}" && pwd -P)
[ -n "${state_volume}" ] || state_volume=${name}-state

case "${browser_port}" in
    ''|*[!0-9]*) usage >&2; exit 2 ;;
esac
[ "${browser_port}" -ge 1 ] && [ "${browser_port}" -le 65535 ] || { usage >&2; exit 2; }
[ -n "${image}" ] && [ -n "${name}" ] && [ -n "${state_volume}" ] && [ -n "${cpus}" ] && [ -n "${memory}" ] || { usage >&2; exit 2; }

if ! container volume inspect "${state_volume}" >/dev/null 2>&1; then
    printf '%s\n' "run.sh: creating persistent VM volume ${state_volume}"
    container volume create --label agentserver.v2.mode=insecure-dev "${state_volume}"
fi

printf '%s\n' "run.sh: starting ${name}; persistent volume ${state_volume}; workspace ${workspace}"
container run \
    --name "${name}" \
    --detach \
    --init \
    --cpus "${cpus}" \
    --memory "${memory}" \
    --publish "127.0.0.1:${browser_port}:17444" \
    --volume "${state_volume}:/var/lib/agentserver" \
    --mount "type=bind,source=${workspace},target=/workspace" \
    --cap-add CAP_CHOWN \
    --cap-add CAP_DAC_OVERRIDE \
    --cap-add CAP_SETUID \
    --cap-add CAP_SETGID \
    "${image}"

attempts=0
until container exec "${name}" test -f /run/agentserver-v2/ready >/dev/null 2>&1; do
    attempts=$((attempts + 1))
    if [ "${attempts}" -ge 120 ]; then
        printf '%s\n' "run.sh: stack was not ready after 120 seconds; recent logs follow" >&2
        container logs "${name}" >&2 || true
        exit 1
    fi
    sleep 1
done

printf '%s\n' "run.sh: ready at https://127.0.0.1:${browser_port}"
printf '%s\n' "run.sh: open the reference web with: ./deploy/insecure-dev/browser-url.sh --name=${name} --browser-port=${browser_port}"
printf '%s\n' "run.sh: run ./deploy/insecure-dev/smoke.sh --name=${name} --browser-port=${browser_port}"
printf '%s\n' "run.sh: stop with: container stop --time 15 ${name}"
