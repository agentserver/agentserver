#!/bin/sh
set -eu

umask 077

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
v2_root=$(CDPATH= cd -- "${script_dir}/../.." && pwd -P)
repository_root=$(CDPATH= cd -- "${v2_root}/.." && pwd -P)

codex_artifact=""
bwrap_artifact=""
service_image=""
harness_image=""
output_directory=""

usage() {
    printf '%s\n' \
        'usage: build-images.sh --codex=/absolute/codex-aarch64-unknown-linux-musl' \
        '                       --bwrap=/absolute/bwrap-aarch64-unknown-linux-musl' \
        '                       --service-image=registry/name:tag' \
        '                       --harness-image=registry/name:tag' \
        '                       --output-dir=/absolute/new-directory'
}

fail() {
    printf '%s\n' "build-images.sh: $*" >&2
    exit 1
}

for argument in "$@"; do
    case "${argument}" in
        --codex=*) codex_artifact=${argument#--codex=} ;;
        --bwrap=*) bwrap_artifact=${argument#--bwrap=} ;;
        --service-image=*) service_image=${argument#--service-image=} ;;
        --harness-image=*) harness_image=${argument#--harness-image=} ;;
        --output-dir=*) output_directory=${argument#--output-dir=} ;;
        --help|-h) usage; exit 0 ;;
        *) usage >&2; exit 2 ;;
    esac
done

case "${codex_artifact}" in /*) ;; *) usage >&2; exit 2 ;; esac
case "${bwrap_artifact}" in /*) ;; *) usage >&2; exit 2 ;; esac
case "${output_directory}" in /*) ;; *) usage >&2; exit 2 ;; esac
[ -n "${service_image}" ] || { usage >&2; exit 2; }
[ -n "${harness_image}" ] || { usage >&2; exit 2; }
[ "${service_image}" != "${harness_image}" ] || fail "service and harness image names must differ"
[ ! -e "${output_directory}" ] || fail "output directory already exists"
[ -d "$(dirname -- "${output_directory}")" ] || fail "output parent is not a directory"

command -v go >/dev/null 2>&1 || fail "go is required"
command -v git >/dev/null 2>&1 || fail "git is required"
command -v container >/dev/null 2>&1 || fail "Apple container CLI is required"
[ "$(go env GOVERSION)" = "go1.26.5" ] || fail "exact Go toolchain go1.26.5 is required"
case "$(container --version)" in
    'container CLI version 1.2.0 '*) ;;
    *) fail "exact Apple container CLI 1.2.0 is required" ;;
esac

git -C "${repository_root}" diff --quiet HEAD -- v2 || fail "tracked v2 source differs from HEAD; commit before a production build"
untracked_v2=$(git -C "${repository_root}" ls-files --others --exclude-standard -- v2)
[ -z "${untracked_v2}" ] || fail "untracked v2 source exists; commit or remove it before a production build"
source_revision=$(git -C "${repository_root}" rev-parse --verify HEAD)
[ "${#source_revision}" -eq 40 ] || fail "HEAD is not a canonical 40-character Git SHA"
case "${source_revision}" in *[!0-9a-f]*) fail "HEAD is not a canonical 40-character Git SHA" ;; esac

work_directory=$(mktemp -d "${TMPDIR:-/tmp}/agentserver-v2-production-images.XXXXXX")
work_directory=$(CDPATH= cd -- "${work_directory}" && pwd -P)
service_verifier_name="agentserver-v2-service-verify-$$"
harness_verifier_name="agentserver-v2-harness-verify-$$"
service_verifier_created=false
harness_verifier_created=false

cleanup() {
    status=$?
    trap - EXIT INT TERM
    if [ "${service_verifier_created}" = true ]; then
        container delete --force "${service_verifier_name}" >/dev/null 2>&1 || true
    fi
    if [ "${harness_verifier_created}" = true ]; then
        container delete --force "${harness_verifier_name}" >/dev/null 2>&1 || true
    fi
    chmod -R u+w "${work_directory}" >/dev/null 2>&1 || true
    rm -rf -- "${work_directory}"
    exit "${status}"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

mkdir -m 0700 "${work_directory}/all-bin" "${work_directory}/service-bin" "${work_directory}/harness-bin"

build_binary() {
    source_command=$1
    output_name=$2
    printf '%s\n' "build-images.sh: compiling ${output_name} from cmd/${source_command}"
    (
        cd "${v2_root}"
        env CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
            go build -buildvcs=false -trimpath -ldflags='-s -w -buildid=' \
            -o "${work_directory}/all-bin/${output_name}" "./cmd/${source_command}"
    )
    chmod 0555 "${work_directory}/all-bin/${output_name}"
}

build_binary agentserver-core agentserver-core
build_binary harness-init agentserver-init
build_binary agentserver-probe agentserver-probe
build_binary browser-gateway browser-gateway
build_binary executor-gateway executor-gateway
build_binary llmproxy llmproxy
build_binary harness-final-exec harness-final-exec
build_binary harness-pool harness-pool
build_binary harness-worker harness-worker

for binary in agentserver-core agentserver-init agentserver-probe browser-gateway executor-gateway llmproxy; do
    cp "${work_directory}/all-bin/${binary}" "${work_directory}/service-bin/${binary}"
done
for binary in agentserver-init agentserver-probe harness-final-exec harness-pool harness-worker; do
    cp "${work_directory}/all-bin/${binary}" "${work_directory}/harness-bin/${binary}"
done
chmod 0555 "${work_directory}/service-bin"/* "${work_directory}/harness-bin"/*

printf '%s\n' "build-images.sh: compiling the host-side closed-world image verifier"
(
    cd "${v2_root}"
    go build -buildvcs=false -trimpath -o "${work_directory}/agentserver-image" ./cmd/agentserver-image
)
chmod 0500 "${work_directory}/agentserver-image"

"${work_directory}/agentserver-image" prepare \
    --kind=service \
    --source-revision="${source_revision}" \
    --binary-dir="${work_directory}/service-bin" \
    --output="${work_directory}/service-payload"
"${work_directory}/agentserver-image" prepare \
    --kind=harness \
    --source-revision="${source_revision}" \
    --binary-dir="${work_directory}/harness-bin" \
    --codex="${codex_artifact}" \
    --bwrap="${bwrap_artifact}" \
    --requirements="${v2_root}/packaging/stockruntime/requirements.toml" \
    --output="${work_directory}/harness-payload"

# Apple container 1.2.0 must discover the default Dockerfile inside its build
# context. An absolute --file path can silently yield a two-byte definition and
# an empty-context cache hit even when that path is below the context root.
cp "${script_dir}/service.Containerfile" "${work_directory}/service-payload/Dockerfile"
cp "${script_dir}/harness.Containerfile" "${work_directory}/harness-payload/Dockerfile"
chmod 0444 \
    "${work_directory}/service-payload/Dockerfile" \
    "${work_directory}/harness-payload/Dockerfile"

printf '%s\n' "build-images.sh: building ${service_image}"
container build \
    --platform linux/arm64 \
    --progress plain \
    --no-cache \
    --build-arg "SOURCE_REVISION=${source_revision}" \
    --tag "${service_image}" \
    "${work_directory}/service-payload"

printf '%s\n' "build-images.sh: building ${harness_image}"
container build \
    --platform linux/arm64 \
    --progress plain \
    --no-cache \
    --build-arg "SOURCE_REVISION=${source_revision}" \
    --tag "${harness_image}" \
    "${work_directory}/harness-payload"

verify_image() {
    kind=$1
    image=$2
    verifier_name=$3
    payload=$4
    archive=$5
    container create --name "${verifier_name}" --no-dns "${image}" /usr/local/bin/agentserver-probe >/dev/null
    case "${kind}" in
        service) service_verifier_created=true ;;
        harness) harness_verifier_created=true ;;
        *) fail "internal unknown image kind" ;;
    esac
    container start "${verifier_name}" >/dev/null
    container export --output "${archive}" "${verifier_name}"
    chmod 0400 "${archive}"
    "${work_directory}/agentserver-image" verify-tar \
        --manifest="${payload}/image-manifest.json" \
        --tar="${archive}"
    container delete "${verifier_name}" >/dev/null
    case "${kind}" in
        service) service_verifier_created=false ;;
        harness) harness_verifier_created=false ;;
    esac
}

verify_image service "${service_image}" "${service_verifier_name}" \
    "${work_directory}/service-payload" "${work_directory}/service-rootfs.tar"
verify_image harness "${harness_image}" "${harness_verifier_name}" \
    "${work_directory}/harness-payload" "${work_directory}/harness-rootfs.tar"

mkdir -m 0700 "${output_directory}"
cp "${work_directory}/service-payload/image-manifest.json" "${output_directory}/service-image-manifest.json"
cp "${work_directory}/harness-payload/image-manifest.json" "${output_directory}/harness-image-manifest.json"
container image inspect "${service_image}" >"${output_directory}/service-image-inspect.json"
container image inspect "${harness_image}" >"${output_directory}/harness-image-inspect.json"
chmod 0444 "${output_directory}"/*

printf '%s\n' "build-images.sh: verified production images"
printf '%s\n' "  service=${service_image}"
printf '%s\n' "  harness=${harness_image}"
printf '%s\n' "  evidence=${output_directory}"
