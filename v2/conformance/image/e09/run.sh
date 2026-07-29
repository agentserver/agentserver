#!/bin/sh
set -eu

e09_fail() {
    printf '%s\n' "E09 image runner: $*" >&2
    exit 2
}

e09_require_executable() {
    e09_label=$1
    e09_path=$2
    case "$e09_path" in
        /*) ;;
        *) e09_fail "$e09_label path must be absolute" ;;
    esac
    [ -f "$e09_path" ] || e09_fail "$e09_label is not a regular file: $e09_path"
    [ -x "$e09_path" ] || e09_fail "$e09_label is not executable: $e09_path"
    [ ! -L "$e09_path" ] || e09_fail "$e09_label path must not be a symlink"
}

e09_require_digest() {
    e09_label=$1
    e09_digest=$2
    case "$e09_digest" in
        *[!0-9a-f]*|'') e09_fail "$e09_label must be lowercase hexadecimal" ;;
    esac
    [ "${#e09_digest}" -eq 64 ] || e09_fail "$e09_label must contain 64 characters"
}

e09_require_size() {
    e09_label=$1
    e09_size=$2
    case "$e09_size" in
        *[!0-9]*|'') e09_fail "$e09_label must be a positive decimal integer" ;;
        0*) e09_fail "$e09_label must be positive and canonical" ;;
    esac
}

e09_script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd -P)
e09_v2_root=$(CDPATH= cd "$e09_script_dir/../../.." && pwd -P)
e09_runtime=${AGENTSERVER_CONTAINER_RUNTIME:-docker}
e09_runtime_name=${e09_runtime##*/}
e09_goarch=${AGENTSERVER_E09_GOARCH:-amd64}
e09_codex=${AGENTSERVER_CODEX_LINUX_BIN:-${AGENTSERVER_CODEX_LINUX_AMD64_BIN:-}}
e09_bwrap=${AGENTSERVER_BWRAP_LINUX_BIN:-${AGENTSERVER_BWRAP_LINUX_AMD64_BIN:-}}
e09_release=${AGENTSERVER_EXPECTED_CODEX_RELEASE:-}
e09_codex_digest=${AGENTSERVER_EXPECTED_CODEX_SHA256:-}
e09_codex_size=${AGENTSERVER_EXPECTED_CODEX_SIZE_BYTES:-}
e09_bwrap_digest=${AGENTSERVER_EXPECTED_BWRAP_SHA256:-}
e09_bwrap_size=${AGENTSERVER_EXPECTED_BWRAP_SIZE_BYTES:-}

case "$e09_goarch" in
    amd64|arm64) ;;
    *) e09_fail "AGENTSERVER_E09_GOARCH must be amd64 or arm64" ;;
esac
e09_platform="linux/$e09_goarch"
e09_runtime_platform="linux-$e09_goarch"

e09_require_executable "Codex candidate" "$e09_codex"
e09_require_executable "bwrap candidate" "$e09_bwrap"
[ -n "$e09_release" ] || e09_fail "AGENTSERVER_EXPECTED_CODEX_RELEASE is required"
e09_require_digest "AGENTSERVER_EXPECTED_CODEX_SHA256" "$e09_codex_digest"
e09_require_size "AGENTSERVER_EXPECTED_CODEX_SIZE_BYTES" "$e09_codex_size"
e09_require_digest "AGENTSERVER_EXPECTED_BWRAP_SHA256" "$e09_bwrap_digest"
e09_require_size "AGENTSERVER_EXPECTED_BWRAP_SIZE_BYTES" "$e09_bwrap_size"
command -v go >/dev/null 2>&1 || e09_fail "go is required to build the Linux conformance binary"
command -v "$e09_runtime" >/dev/null 2>&1 || e09_fail "container runtime not found: $e09_runtime"
cd "$e09_v2_root"

if [ "$e09_runtime_name" = "container" ]; then
    e09_host_arch=$(uname -m)
    case "$e09_host_arch" in
        x86_64) e09_host_goarch=amd64 ;;
        arm64|aarch64) e09_host_goarch=arm64 ;;
        *) e09_fail "unsupported Apple container host architecture: $e09_host_arch" ;;
    esac
    [ "$e09_goarch" = "$e09_host_goarch" ] || \
        e09_fail "positive gate refuses cross-architecture emulation: requested $e09_goarch on $e09_host_goarch"
    # Apple container's builder currently needs a workspace-backed context.
    e09_tmp_parent=$e09_v2_root
    e09_tmp_prefix=.agentserver-v2-e09
else
    e09_tmp_parent=${TMPDIR:-/tmp}
    e09_tmp_prefix=agentserver-v2-e09
fi
[ -d "$e09_tmp_parent" ] || e09_fail "temporary directory does not exist: $e09_tmp_parent"
e09_tmp_parent=$(CDPATH= cd "$e09_tmp_parent" && pwd -P)
e09_work=$(mktemp -d "$e09_tmp_parent/$e09_tmp_prefix.XXXXXX")
e09_context="$e09_work/context"
e09_tag_suffix=${e09_work##*.}
e09_tag_suffix=$(printf '%s' "$e09_tag_suffix" | tr '[:upper:]' '[:lower:]')
e09_tag="agentserver-v2-e09-$e09_goarch-$e09_tag_suffix"
e09_image_built=0

e09_cleanup() {
    if [ "$e09_image_built" -eq 1 ]; then
        "$e09_runtime" image rm "$e09_tag" >/dev/null 2>&1 || true
    fi
    case "$e09_work" in
        "$e09_tmp_parent"/"$e09_tmp_prefix".*) rm -rf -- "$e09_work" ;;
        *) printf '%s\n' "E09 image runner: refusing unsafe cleanup path $e09_work" >&2 ;;
    esac
}
trap e09_cleanup EXIT HUP INT TERM

mkdir "$e09_context"
cp "$e09_script_dir/Dockerfile" "$e09_context/Dockerfile"
cp "$e09_codex" "$e09_context/codex"
cp "$e09_bwrap" "$e09_context/bwrap"
env GOCACHE="$e09_work/go-cache" GOOS=linux GOARCH="$e09_goarch" CGO_ENABLED=0 \
    go test -c -o "$e09_context/conformance.test" ./conformance/codex
chmod 0555 "$e09_context/codex" "$e09_context/bwrap" "$e09_context/conformance.test"

if [ "$e09_runtime_name" = "container" ]; then
    "$e09_runtime" build \
        --no-cache \
        --platform "$e09_platform" \
        --tag "$e09_tag" \
        "$e09_context"
else
    "$e09_runtime" build \
        --network none \
        --no-cache \
        --platform "$e09_platform" \
        --tag "$e09_tag" \
        "$e09_context"
fi
e09_image_built=1

if [ "$e09_runtime_name" = "container" ]; then
    "$e09_runtime" run \
        --rm \
        --platform "$e09_platform" \
        --network none \
        --no-dns \
        --read-only \
        --user 65532:65532 \
        --cap-drop ALL \
        --memory 1G \
        --tmpfs /tmp \
        --env AGENTSERVER_RUN_LIVE_CODEX=1 \
        --env AGENTSERVER_RUN_IMAGE_E09=1 \
        --env AGENTSERVER_CODEX_BIN=/opt/agentserver/runtime/bin/codex \
        --env "AGENTSERVER_EXPECTED_RUNTIME_PLATFORM=$e09_runtime_platform" \
        --env "AGENTSERVER_EXPECTED_CODEX_RELEASE=$e09_release" \
        --env "AGENTSERVER_EXPECTED_CODEX_SHA256=$e09_codex_digest" \
        --env "AGENTSERVER_EXPECTED_CODEX_SIZE_BYTES=$e09_codex_size" \
        --env "AGENTSERVER_EXPECTED_BWRAP_SHA256=$e09_bwrap_digest" \
        --env "AGENTSERVER_EXPECTED_BWRAP_SIZE_BYTES=$e09_bwrap_size" \
        "$e09_tag" \
        -test.run '^TestExecServerE09BundledBwrapImageGate$' \
        -test.count 1 \
        -test.timeout 120s \
        -test.v
else
    "$e09_runtime" run \
        --rm \
        --platform "$e09_platform" \
        --network none \
        --read-only \
        --user 65532:65532 \
        --cap-drop ALL \
        --security-opt no-new-privileges \
        --pids-limit 256 \
        --memory 1g \
        --memory-swap 1g \
        --tmpfs /tmp:rw,nosuid,nodev,size=256m,mode=1777 \
        --env AGENTSERVER_RUN_LIVE_CODEX=1 \
        --env AGENTSERVER_RUN_IMAGE_E09=1 \
        --env AGENTSERVER_CODEX_BIN=/opt/agentserver/runtime/bin/codex \
        --env "AGENTSERVER_EXPECTED_RUNTIME_PLATFORM=$e09_runtime_platform" \
        --env "AGENTSERVER_EXPECTED_CODEX_RELEASE=$e09_release" \
        --env "AGENTSERVER_EXPECTED_CODEX_SHA256=$e09_codex_digest" \
        --env "AGENTSERVER_EXPECTED_CODEX_SIZE_BYTES=$e09_codex_size" \
        --env "AGENTSERVER_EXPECTED_BWRAP_SHA256=$e09_bwrap_digest" \
        --env "AGENTSERVER_EXPECTED_BWRAP_SIZE_BYTES=$e09_bwrap_size" \
        "$e09_tag" \
        -test.run '^TestExecServerE09BundledBwrapImageGate$' \
        -test.count 1 \
        -test.timeout 120s \
        -test.v
fi
