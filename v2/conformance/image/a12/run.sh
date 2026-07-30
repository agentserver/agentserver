#!/bin/sh
set -eu

a12_fail() {
    printf '%s\n' "A12 image runner: $*" >&2
    exit 2
}

a12_require_executable() {
    a12_label=$1
    a12_path=$2
    case "$a12_path" in
        /*) ;;
        *) a12_fail "$a12_label path must be absolute" ;;
    esac
    [ -f "$a12_path" ] || a12_fail "$a12_label is not a regular file: $a12_path"
    [ -x "$a12_path" ] || a12_fail "$a12_label is not executable: $a12_path"
    [ ! -L "$a12_path" ] || a12_fail "$a12_label path must not be a symlink"
}

a12_require_digest() {
    a12_label=$1
    a12_digest=$2
    case "$a12_digest" in
        *[!0-9a-f]*|'') a12_fail "$a12_label must be lowercase hexadecimal" ;;
    esac
    [ "${#a12_digest}" -eq 64 ] || a12_fail "$a12_label must contain 64 characters"
}

a12_require_size() {
    a12_label=$1
    a12_size=$2
    case "$a12_size" in
        *[!0-9]*|'') a12_fail "$a12_label must be a positive decimal integer" ;;
        0*) a12_fail "$a12_label must be positive and canonical" ;;
    esac
}

a12_script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd -P)
a12_v2_root=$(CDPATH= cd "$a12_script_dir/../../.." && pwd -P)
a12_runtime=${AGENTSERVER_CONTAINER_RUNTIME:-docker}
a12_runtime_name=${a12_runtime##*/}
a12_goarch=${AGENTSERVER_A12_GOARCH:-amd64}
a12_codex=${AGENTSERVER_CODEX_LINUX_BIN:-${AGENTSERVER_CODEX_LINUX_AMD64_BIN:-}}
a12_release=${AGENTSERVER_EXPECTED_CODEX_RELEASE:-}
a12_digest=${AGENTSERVER_EXPECTED_CODEX_SHA256:-}
a12_size=${AGENTSERVER_EXPECTED_CODEX_SIZE_BYTES:-}

case "$a12_goarch" in
    amd64|arm64) ;;
    *) a12_fail "AGENTSERVER_A12_GOARCH must be amd64 or arm64" ;;
esac
a12_platform="linux/$a12_goarch"
a12_runtime_platform="linux-$a12_goarch"

a12_require_executable "Codex candidate" "$a12_codex"
[ -n "$a12_release" ] || a12_fail "AGENTSERVER_EXPECTED_CODEX_RELEASE is required"
a12_require_digest "AGENTSERVER_EXPECTED_CODEX_SHA256" "$a12_digest"
a12_require_size "AGENTSERVER_EXPECTED_CODEX_SIZE_BYTES" "$a12_size"
command -v go >/dev/null 2>&1 || a12_fail "go is required to build the Linux conformance binary"
command -v "$a12_runtime" >/dev/null 2>&1 || a12_fail "container runtime not found: $a12_runtime"
cd "$a12_v2_root"

if [ "$a12_runtime_name" = "container" ]; then
    a12_host_arch=$(uname -m)
    case "$a12_host_arch" in
        x86_64) a12_host_goarch=amd64 ;;
        arm64|aarch64) a12_host_goarch=arm64 ;;
        *) a12_fail "unsupported Apple container host architecture: $a12_host_arch" ;;
    esac
    [ "$a12_goarch" = "$a12_host_goarch" ] || \
        a12_fail "positive gate refuses cross-architecture emulation: requested $a12_goarch on $a12_host_goarch"
    a12_tmp_parent=$a12_v2_root
    a12_tmp_prefix=.agentserver-v2-a12
else
    a12_tmp_parent=${TMPDIR:-/tmp}
    a12_tmp_prefix=agentserver-v2-a12
fi
[ -d "$a12_tmp_parent" ] || a12_fail "temporary directory does not exist: $a12_tmp_parent"
a12_tmp_parent=$(CDPATH= cd "$a12_tmp_parent" && pwd -P)
a12_work=$(mktemp -d "$a12_tmp_parent/$a12_tmp_prefix.XXXXXX")
a12_context="$a12_work/context"
a12_tag_suffix=${a12_work##*.}
a12_tag_suffix=$(printf '%s' "$a12_tag_suffix" | tr '[:upper:]' '[:lower:]')
a12_tag="agentserver-v2-a12-$a12_goarch-$a12_tag_suffix"
a12_image_built=0

a12_cleanup() {
    if [ "$a12_image_built" -eq 1 ]; then
        "$a12_runtime" image rm "$a12_tag" >/dev/null 2>&1 || true
    fi
    case "$a12_work" in
        "$a12_tmp_parent"/"$a12_tmp_prefix".*) rm -rf -- "$a12_work" ;;
        *) printf '%s\n' "A12 image runner: refusing unsafe cleanup path $a12_work" >&2 ;;
    esac
}
trap a12_cleanup EXIT HUP INT TERM

mkdir "$a12_context"
cp "$a12_script_dir/Dockerfile" "$a12_context/Dockerfile"
cp "$a12_script_dir/mount-anchor" "$a12_context/mount-anchor"
cp "$a12_codex" "$a12_context/codex"
env GOCACHE="$a12_work/go-cache" GOOS=linux GOARCH="$a12_goarch" CGO_ENABLED=0 \
    go test -c -o "$a12_context/conformance.test" ./conformance/codex
chmod 0555 "$a12_context/codex" "$a12_context/conformance.test"

if [ "$a12_runtime_name" = "container" ]; then
    "$a12_runtime" build \
        --no-cache \
        --platform "$a12_platform" \
        --tag "$a12_tag" \
        "$a12_context"
else
    "$a12_runtime" build \
        --network none \
        --no-cache \
        --platform "$a12_platform" \
        --tag "$a12_tag" \
        "$a12_context"
fi
a12_image_built=1

if [ "$a12_runtime_name" = "container" ]; then
    "$a12_runtime" run \
        --rm \
        --platform "$a12_platform" \
        --network none \
        --no-dns \
        --read-only \
        --user 0:0 \
        --cap-drop ALL \
        --cap-add NET_ADMIN \
        --cap-add CHOWN \
        --cap-add DAC_READ_SEARCH \
        --cap-add SETUID \
        --cap-add SETGID \
        --cap-add SYS_ADMIN \
        --memory 1G \
        --tmpfs /tmp \
        --tmpfs /run/agentserver \
        --env AGENTSERVER_RUN_LIVE_CODEX=1 \
        --env AGENTSERVER_RUN_IMAGE_A12=1 \
        --env AGENTSERVER_HARDEN_A12_TMPFS=1 \
        --env AGENTSERVER_CODEX_BIN=/opt/agentserver/runtime/bin/codex \
        --env "AGENTSERVER_EXPECTED_RUNTIME_PLATFORM=$a12_runtime_platform" \
        --env "AGENTSERVER_EXPECTED_CODEX_RELEASE=$a12_release" \
        --env "AGENTSERVER_EXPECTED_CODEX_SHA256=$a12_digest" \
        --env "AGENTSERVER_EXPECTED_CODEX_SIZE_BYTES=$a12_size" \
        "$a12_tag" \
        -test.run '^TestAppServerA12ProductionIsolationImageGate$' \
        -test.count 1 \
        -test.timeout 240s \
        -test.v
else
    "$a12_runtime" run \
        --rm \
        --platform "$a12_platform" \
        --network none \
        --read-only \
        --user 0:0 \
        --cap-drop ALL \
        --cap-add NET_ADMIN \
        --cap-add CHOWN \
        --cap-add DAC_READ_SEARCH \
        --cap-add SETUID \
        --cap-add SETGID \
        --security-opt no-new-privileges \
        --pids-limit 256 \
        --memory 1g \
        --memory-swap 1g \
        --tmpfs /tmp:rw,nosuid,nodev,noexec,size=512m,mode=1777 \
        --tmpfs /run/agentserver:rw,nosuid,nodev,noexec,size=16m,mode=0755 \
        --env AGENTSERVER_RUN_LIVE_CODEX=1 \
        --env AGENTSERVER_RUN_IMAGE_A12=1 \
        --env AGENTSERVER_CODEX_BIN=/opt/agentserver/runtime/bin/codex \
        --env "AGENTSERVER_EXPECTED_RUNTIME_PLATFORM=$a12_runtime_platform" \
        --env "AGENTSERVER_EXPECTED_CODEX_RELEASE=$a12_release" \
        --env "AGENTSERVER_EXPECTED_CODEX_SHA256=$a12_digest" \
        --env "AGENTSERVER_EXPECTED_CODEX_SIZE_BYTES=$a12_size" \
        "$a12_tag" \
        -test.run '^TestAppServerA12ProductionIsolationImageGate$' \
        -test.count 1 \
        -test.timeout 240s \
        -test.v
fi
