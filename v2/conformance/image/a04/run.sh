#!/bin/sh
set -eu

a04_fail() {
    printf '%s\n' "A04 image runner: $*" >&2
    exit 2
}

a04_script_dir=$(CDPATH= cd "$(dirname "$0")" && pwd -P)
a04_v2_root=$(CDPATH= cd "$a04_script_dir/../../.." && pwd -P)
a04_runtime=${AGENTSERVER_CONTAINER_RUNTIME:-docker}
a04_runtime_name=${a04_runtime##*/}
a04_codex=${AGENTSERVER_CODEX_LINUX_AMD64_BIN:-}
a04_release=${AGENTSERVER_EXPECTED_CODEX_RELEASE:-}
a04_digest=${AGENTSERVER_EXPECTED_CODEX_SHA256:-}
a04_size=${AGENTSERVER_EXPECTED_CODEX_SIZE_BYTES:-}

case "$a04_codex" in
    /*) ;;
    *) a04_fail "AGENTSERVER_CODEX_LINUX_AMD64_BIN must be an absolute path" ;;
esac
[ -f "$a04_codex" ] || a04_fail "Codex candidate is not a regular file: $a04_codex"
[ -x "$a04_codex" ] || a04_fail "Codex candidate is not executable: $a04_codex"
[ ! -L "$a04_codex" ] || a04_fail "Codex candidate path must not be a symlink"
[ -n "$a04_release" ] || a04_fail "AGENTSERVER_EXPECTED_CODEX_RELEASE is required"
case "$a04_digest" in
    *[!0-9a-f]*|'') a04_fail "AGENTSERVER_EXPECTED_CODEX_SHA256 must be lowercase hexadecimal" ;;
esac
[ "${#a04_digest}" -eq 64 ] || a04_fail "AGENTSERVER_EXPECTED_CODEX_SHA256 must contain 64 characters"
case "$a04_size" in
    *[!0-9]*|'') a04_fail "AGENTSERVER_EXPECTED_CODEX_SIZE_BYTES must be a positive decimal integer" ;;
    0*) a04_fail "AGENTSERVER_EXPECTED_CODEX_SIZE_BYTES must be positive and canonical" ;;
esac
command -v go >/dev/null 2>&1 || a04_fail "go is required to build the Linux conformance binary"
command -v "$a04_runtime" >/dev/null 2>&1 || a04_fail "container runtime not found: $a04_runtime"
cd "$a04_v2_root"

if [ "$a04_runtime_name" = "container" ]; then
    # Apple container's builder must receive a workspace-backed context on the
    # current macOS implementation. Temporary contexts under /private/tmp can
    # otherwise arrive at the builder as an empty transfer.
    a04_tmp_parent=$a04_v2_root
    a04_tmp_prefix=.agentserver-v2-a04
else
    a04_tmp_parent=${TMPDIR:-/tmp}
    a04_tmp_prefix=agentserver-v2-a04
fi
[ -d "$a04_tmp_parent" ] || a04_fail "temporary directory does not exist: $a04_tmp_parent"
a04_tmp_parent=$(CDPATH= cd "$a04_tmp_parent" && pwd -P)
a04_work=$(mktemp -d "$a04_tmp_parent/$a04_tmp_prefix.XXXXXX")
a04_context="$a04_work/context"
a04_tag_suffix=${a04_work##*.}
a04_tag_suffix=$(printf '%s' "$a04_tag_suffix" | tr '[:upper:]' '[:lower:]')
a04_tag="agentserver-v2-a04-$a04_tag_suffix"
a04_image_built=0

a04_cleanup() {
    if [ "$a04_image_built" -eq 1 ]; then
        "$a04_runtime" image rm "$a04_tag" >/dev/null 2>&1 || true
    fi
    case "$a04_work" in
        "$a04_tmp_parent"/"$a04_tmp_prefix".*) rm -rf -- "$a04_work" ;;
        *) printf '%s\n' "A04 image runner: refusing unsafe cleanup path $a04_work" >&2 ;;
    esac
}
trap a04_cleanup EXIT HUP INT TERM

mkdir "$a04_context"
cp "$a04_script_dir/Dockerfile" "$a04_context/Dockerfile"
cp "$a04_codex" "$a04_context/codex"
env GOCACHE="$a04_work/go-cache" GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
    go test -c -o "$a04_context/conformance.test" ./conformance/codex
chmod 0555 "$a04_context/codex" "$a04_context/conformance.test"

if [ "$a04_runtime_name" = "container" ]; then
    "$a04_runtime" build \
        --no-cache \
        --platform linux/amd64 \
        --tag "$a04_tag" \
        "$a04_context"
else
    "$a04_runtime" build \
        --network none \
        --no-cache \
        --platform linux/amd64 \
        --tag "$a04_tag" \
        "$a04_context"
fi
a04_image_built=1

if [ "$a04_runtime_name" = "container" ]; then
    # Apple container does not accept Docker's tmpfs mount-option syntax. Give
    # the test only CAP_SYS_ADMIN so it can remount the already-isolated
    # /etc/codex tmpfs nodev,nosuid,noexec, then verify mountinfo itself.
    "$a04_runtime" run \
        --rm \
        --platform linux/amd64 \
        --network none \
        --no-dns \
        --read-only \
        --user 0:0 \
        --cap-drop ALL \
        --cap-add SYS_ADMIN \
        --memory 1G \
        --tmpfs /tmp \
        --tmpfs /etc/codex \
        --env AGENTSERVER_RUN_LIVE_CODEX=1 \
        --env AGENTSERVER_RUN_IMAGE_A04=1 \
        --env AGENTSERVER_HARDEN_A04_TMPFS=1 \
        --env AGENTSERVER_CODEX_BIN=/opt/agentserver/codex \
        --env "AGENTSERVER_EXPECTED_CODEX_RELEASE=$a04_release" \
        --env "AGENTSERVER_EXPECTED_CODEX_SHA256=$a04_digest" \
        --env "AGENTSERVER_EXPECTED_CODEX_SIZE_BYTES=$a04_size" \
        "$a04_tag" \
        -test.run '^TestAppServerA04SystemRequirementsEndpointAllowlist$' \
        -test.count 1 \
        -test.timeout 90s \
        -test.v
else
    "$a04_runtime" run \
        --rm \
        --platform linux/amd64 \
        --network none \
        --read-only \
        --user 0:0 \
        --cap-drop ALL \
        --security-opt no-new-privileges \
        --pids-limit 256 \
        --memory 1g \
        --memory-swap 1g \
        --tmpfs /tmp:rw,nosuid,nodev,noexec,size=512m,mode=1777 \
        --tmpfs /etc/codex:rw,nosuid,nodev,noexec,size=64k,mode=0755 \
        --env AGENTSERVER_RUN_LIVE_CODEX=1 \
        --env AGENTSERVER_RUN_IMAGE_A04=1 \
        --env AGENTSERVER_CODEX_BIN=/opt/agentserver/codex \
        --env "AGENTSERVER_EXPECTED_CODEX_RELEASE=$a04_release" \
        --env "AGENTSERVER_EXPECTED_CODEX_SHA256=$a04_digest" \
        --env "AGENTSERVER_EXPECTED_CODEX_SIZE_BYTES=$a04_size" \
        "$a04_tag" \
        -test.run '^TestAppServerA04SystemRequirementsEndpointAllowlist$' \
        -test.count 1 \
        -test.timeout 90s \
        -test.v
fi
