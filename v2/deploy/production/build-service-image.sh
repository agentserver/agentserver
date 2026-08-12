#!/bin/sh
set -eu

umask 077

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
v2_root=$(CDPATH= cd -- "${script_dir}/../.." && pwd -P)
repository_root=$(CDPATH= cd -- "${v2_root}/.." && pwd -P)

platform=""
service_image=""
output_directory=""
cache="none"

usage() {
    printf '%s\n' \
        'usage: build-service-image.sh --platform=linux-amd64|linux-arm64' \
        '                              --service-image=registry/name:tag' \
        '                              --output-dir=/absolute/new-directory' \
        '                              [--cache=none|gha]'
}

fail() {
    printf '%s\n' "build-service-image.sh: $*" >&2
    exit 1
}

for argument in "$@"; do
    case "${argument}" in
        --platform=*) platform=${argument#--platform=} ;;
        --service-image=*) service_image=${argument#--service-image=} ;;
        --output-dir=*) output_directory=${argument#--output-dir=} ;;
        --cache=*) cache=${argument#--cache=} ;;
        --help|-h) usage; exit 0 ;;
        *) usage >&2; exit 2 ;;
    esac
done

case "${platform}" in
    linux-amd64) goarch=amd64 ;;
    linux-arm64) goarch=arm64 ;;
    *) usage >&2; exit 2 ;;
esac
oci_platform="linux/${goarch}"

[ -n "${service_image}" ] || { usage >&2; exit 2; }
case "${output_directory}" in /*) ;; *) usage >&2; exit 2 ;; esac
case "${cache}" in none|gha) ;; *) usage >&2; exit 2 ;; esac
[ ! -e "${output_directory}" ] || fail "output directory already exists"
[ -d "$(dirname -- "${output_directory}")" ] || fail "output parent is not a directory"

command -v docker >/dev/null 2>&1 || fail "Docker CLI is required"
command -v git >/dev/null 2>&1 || fail "git is required"
command -v go >/dev/null 2>&1 || fail "go is required"
command -v jq >/dev/null 2>&1 || fail "jq is required"
command -v tar >/dev/null 2>&1 || fail "tar is required"
docker buildx version >/dev/null 2>&1 || fail "Docker buildx is required"
[ "$(go env GOVERSION)" = "go1.26.5" ] || fail "exact Go toolchain go1.26.5 is required"
[ "$(uname -s)" = "Linux" ] || fail "developer service releases run only on Linux"

git -C "${repository_root}" diff --quiet HEAD -- v2 || fail "tracked v2 source differs from HEAD; commit before publishing"
untracked_v2=$(git -C "${repository_root}" ls-files --others --exclude-standard -- v2)
[ -z "${untracked_v2}" ] || fail "untracked v2 source exists; commit or remove it before publishing"
source_revision=$(git -C "${repository_root}" rev-parse --verify HEAD)
[ "${#source_revision}" -eq 40 ] || fail "HEAD is not a canonical 40-character Git SHA"
case "${source_revision}" in *[!0-9a-f]*) fail "HEAD is not a canonical 40-character Git SHA" ;; esac

work_directory=$(mktemp -d "${TMPDIR:-/tmp}/agentserver-v2-service-image.XXXXXX")
work_directory=$(CDPATH= cd -- "${work_directory}" && pwd -P)

cleanup() {
    status=$?
    trap - EXIT INT TERM
    chmod -R u+w "${work_directory}" >/dev/null 2>&1 || true
    rm -rf -- "${work_directory}"
    exit "${status}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

mkdir -m 0700 \
    "${work_directory}/service-bin" \
    "${work_directory}/service-context"

printf '%s\n' 'build-service-image.sh: compiling root service binaries in one Go build'
(
    cd "${v2_root}"
    env CGO_ENABLED=0 GOOS=linux GOARCH="${goarch}" GOBIN="${work_directory}/service-bin" \
        go install -buildvcs=false -trimpath -ldflags='-s -w -buildid=' \
        ./cmd/agentserver-core \
        ./cmd/agentserver-probe \
        ./cmd/platform-gateway \
        ./cmd/browser-gateway \
        ./cmd/executor-gateway \
        ./cmd/egress-authorizer \
        ./cmd/llmproxy
)
printf '%s\n' 'build-service-image.sh: compiling sandbox-gateway'
(
    cd "${v2_root}/providers/tae"
    env CGO_ENABLED=0 GOOS=linux GOARCH="${goarch}" \
        go build -buildvcs=false -trimpath -ldflags='-s -w -buildid=' \
        -o "${work_directory}/service-bin/sandbox-gateway" ./cmd/sandbox-gateway
)
chmod 0555 "${work_directory}/service-bin"/*

printf '%s\n' 'build-service-image.sh: preparing the closed-world service rootfs'
(
    cd "${v2_root}"
    go build -buildvcs=false -trimpath -o "${work_directory}/agentserver-image" ./cmd/agentserver-image
)
chmod 0500 "${work_directory}/agentserver-image"
"${work_directory}/agentserver-image" prepare \
    --kind=service \
    --platform="${platform}" \
    --source-revision="${source_revision}" \
    --binary-dir="${work_directory}/service-bin" \
    --output="${work_directory}/service-payload"

tar --format=ustar --no-xattrs -cf "${work_directory}/service-context/rootfs.tar" \
    -C "${work_directory}/service-payload/rootfs" .
cp "${script_dir}/service.Containerfile" "${work_directory}/service-context/Dockerfile"
chmod 0444 \
    "${work_directory}/service-context/Dockerfile" \
    "${work_directory}/service-context/rootfs.tar"

set -- docker buildx build \
    --platform "${oci_platform}" \
    --progress plain \
    --provenance=false \
    --sbom=false \
    --build-arg "SOURCE_REVISION=${source_revision}" \
    --tag "${service_image}" \
    --push \
    --metadata-file "${work_directory}/build-metadata.json"
if [ "${cache}" = "gha" ]; then
    set -- "$@" \
        --cache-from "type=gha,scope=agentserver-v2-service-${platform}" \
        --cache-to "type=gha,mode=max,scope=agentserver-v2-service-${platform}"
fi
set -- "$@" "${work_directory}/service-context"

printf '%s\n' "build-service-image.sh: building and publishing ${service_image}"
"$@"
service_digest=$(jq -er '."containerimage.digest"' "${work_directory}/build-metadata.json")
[ "${#service_digest}" -eq 71 ] || fail "BuildKit did not return a canonical service manifest digest"
case "${service_digest}" in sha256:*) ;; *) fail "BuildKit did not return a canonical service manifest digest" ;; esac
case "${service_digest#sha256:}" in *[!0-9a-f]*) fail "BuildKit did not return a canonical service manifest digest" ;; esac
docker buildx imagetools inspect "${service_image}@${service_digest}" >/dev/null

mkdir -m 0700 "${output_directory}"
cp "${work_directory}/service-payload/image-manifest.json" "${output_directory}/service-image-manifest.json"
printf '%s\n' "${service_digest}" >"${output_directory}/service-image-digest.txt"
chmod 0444 "${output_directory}"/*

printf '%s\n' \
    'build-service-image.sh: developer service image published' \
    "  platform=${platform}" \
    "  image=${service_image}@${service_digest}" \
    "  source_revision=${source_revision}"
