#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
v2_root=$(CDPATH= cd -- "${script_dir}/../.." && pwd -P)
image=agentserver-v2-insecure-dev:0.146.0
codex=""
bwrap=""
agentx_source=""

usage() {
    cat <<'EOF'
usage: build.sh --codex=/absolute/codex-aarch64-unknown-linux-musl \
                --bwrap=/absolute/bwrap-aarch64-unknown-linux-musl \
                --agentx-source=/absolute/agentx-v2 [--image=name:tag]
EOF
}

for argument in "$@"; do
    case "${argument}" in
        --codex=*) codex=${argument#--codex=} ;;
        --bwrap=*) bwrap=${argument#--bwrap=} ;;
        --agentx-source=*) agentx_source=${argument#--agentx-source=} ;;
        --image=*) image=${argument#--image=} ;;
        --help|-h) usage; exit 0 ;;
        *) usage >&2; exit 2 ;;
    esac
done

case "${codex}" in /*) ;; *) usage >&2; exit 2 ;; esac
case "${bwrap}" in /*) ;; *) usage >&2; exit 2 ;; esac
case "${agentx_source}" in /*) ;; *) usage >&2; exit 2 ;; esac
[ -f "${codex}" ] || { printf '%s\n' "build.sh: Codex artifact is not a file" >&2; exit 1; }
[ -f "${bwrap}" ] || { printf '%s\n' "build.sh: bwrap artifact is not a file" >&2; exit 1; }
[ -f "${agentx_source}/go.mod" ] || { printf '%s\n' "build.sh: agentx source is not a Go module" >&2; exit 1; }
[ -n "${image}" ] || { printf '%s\n' "build.sh: image tag is empty" >&2; exit 2; }

build_context=$(mktemp -d "${TMPDIR:-/tmp}/agentserver-v2-image.XXXXXX")
cleanup() {
    rm -rf -- "${build_context}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

mkdir -p "${build_context}/bin" "${build_context}/stock"
cp "${script_dir}/Containerfile" "${build_context}/Containerfile"
cp "${script_dir}/entrypoint.sh" "${build_context}/entrypoint.sh"
cp "${script_dir}/requirements.toml" "${build_context}/requirements.toml"
cp "${codex}" "${build_context}/stock/codex"
cp "${bwrap}" "${build_context}/stock/bwrap"
chmod 0555 "${build_context}/stock/codex" "${build_context}/stock/bwrap" "${build_context}/entrypoint.sh"
chmod 0444 "${build_context}/requirements.toml"

for command in \
    agentserver-core \
    agentserver-dev \
    browser-gateway \
    executor-gateway \
    harness-final-exec \
    harness-pool \
    harness-worker
do
    printf '%s\n' "build.sh: compiling ${command} for linux/arm64"
    (
        cd "${v2_root}"
        env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
            go build -buildvcs=false -trimpath -o "${build_context}/bin/${command}" "./cmd/${command}"
    )
done

printf '%s\n' "build.sh: compiling independent agentx for linux/arm64"
(
    cd "${agentx_source}"
    env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
        go build -buildvcs=false -trimpath -o "${build_context}/bin/agentx" ./cmd/agentx
)
chmod 0555 "${build_context}"/bin/*

printf '%s\n' "build.sh: assembling ${image} from pinned artifacts"
container build \
    --platform linux/arm64 \
    --progress plain \
    --file "${build_context}/Containerfile" \
    --tag "${image}" \
    "${build_context}"

printf '%s\n' "build.sh: created ${image}"
