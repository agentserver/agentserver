#!/bin/sh
set -eu

umask 077

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
v2_root=$(CDPATH= cd -- "${script_dir}/../.." && pwd -P)
repository_root=$(CDPATH= cd -- "${v2_root}/.." && pwd -P)

codex_artifact=""
bwrap_artifact=""
lark_cli_artifact=""
lark_skill_artifact=""
bkectl_artifact=""
bkectl_skill_root=""
managed_skill_artifact=""
platform=""
service_image=""
harness_image=""
managed_sandbox_image=""
output_directory=""
engine="apple-container"

usage() {
    printf '%s\n' \
        'usage: build-images.sh --platform=linux-amd64|linux-arm64' \
        '                       --codex=/absolute/codex-ARCH-unknown-linux-musl' \
        '                       --bwrap=/absolute/bwrap-ARCH-unknown-linux-musl' \
        '                       --lark-cli=/absolute/lark-cli' \
        '                       --lark-skill=/absolute/SKILL.md' \
        '                       --bkectl=/absolute/bkectl' \
        '                       --bkectl-skill-root=/absolute/bkectl-skill' \
        '                       --managed-skill=/absolute/SKILL.md' \
        '                       --service-image=registry/name:tag' \
        '                       --harness-image=registry/name:tag' \
        '                       --managed-sandbox-image=registry/name:tag' \
        '                       --engine=apple-container|docker-buildx' \
        '                       --output-dir=/absolute/new-directory'
}

fail() {
    printf '%s\n' "build-images.sh: $*" >&2
    exit 1
}

for argument in "$@"; do
    case "${argument}" in
        --platform=*) platform=${argument#--platform=} ;;
        --codex=*) codex_artifact=${argument#--codex=} ;;
        --bwrap=*) bwrap_artifact=${argument#--bwrap=} ;;
        --lark-cli=*) lark_cli_artifact=${argument#--lark-cli=} ;;
        --lark-skill=*) lark_skill_artifact=${argument#--lark-skill=} ;;
        --bkectl=*) bkectl_artifact=${argument#--bkectl=} ;;
        --bkectl-skill-root=*) bkectl_skill_root=${argument#--bkectl-skill-root=} ;;
        --managed-skill=*) managed_skill_artifact=${argument#--managed-skill=} ;;
        --service-image=*) service_image=${argument#--service-image=} ;;
        --harness-image=*) harness_image=${argument#--harness-image=} ;;
        --managed-sandbox-image=*) managed_sandbox_image=${argument#--managed-sandbox-image=} ;;
        --engine=*) engine=${argument#--engine=} ;;
        --output-dir=*) output_directory=${argument#--output-dir=} ;;
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

case "${codex_artifact}" in /*) ;; *) usage >&2; exit 2 ;; esac
case "${bwrap_artifact}" in /*) ;; *) usage >&2; exit 2 ;; esac
case "${lark_cli_artifact}" in /*) ;; *) usage >&2; exit 2 ;; esac
case "${lark_skill_artifact}" in /*) ;; *) usage >&2; exit 2 ;; esac
case "${bkectl_artifact}" in /*) ;; *) usage >&2; exit 2 ;; esac
case "${bkectl_skill_root}" in /*) ;; *) usage >&2; exit 2 ;; esac
case "${managed_skill_artifact}" in /*) ;; *) usage >&2; exit 2 ;; esac
case "${output_directory}" in /*) ;; *) usage >&2; exit 2 ;; esac
[ -n "${service_image}" ] || { usage >&2; exit 2; }
[ -n "${harness_image}" ] || { usage >&2; exit 2; }
[ -n "${managed_sandbox_image}" ] || { usage >&2; exit 2; }
[ "${service_image}" != "${harness_image}" ] || fail "all three image names must differ"
[ "${service_image}" != "${managed_sandbox_image}" ] || fail "all three image names must differ"
[ "${harness_image}" != "${managed_sandbox_image}" ] || fail "all three image names must differ"
[ ! -e "${output_directory}" ] || fail "output directory already exists"
[ -d "$(dirname -- "${output_directory}")" ] || fail "output parent is not a directory"

command -v go >/dev/null 2>&1 || fail "go is required"
command -v git >/dev/null 2>&1 || fail "git is required"
command -v tar >/dev/null 2>&1 || fail "tar is required"
[ "$(go env GOVERSION)" = "go1.26.5" ] || fail "exact Go toolchain go1.26.5 is required"
case "${engine}" in
    apple-container)
		command -v container >/dev/null 2>&1 || fail "Apple container CLI is required"
		case "$(container --version)" in
			'container CLI version 1.2.2 '*) ;;
			*) fail "exact Apple container CLI 1.2.2 is required" ;;
		esac
		;;
    docker-buildx)
		command -v docker >/dev/null 2>&1 || fail "Docker CLI is required"
		docker buildx version >/dev/null 2>&1 || fail "Docker buildx is required"
		[ "$(uname -s)" = "Linux" ] || fail "docker-buildx production builds run only on Linux"
		;;
    *) usage >&2; exit 2 ;;
esac

git -C "${repository_root}" diff --quiet HEAD -- v2 || fail "tracked v2 source differs from HEAD; commit before a production build"
untracked_v2=$(git -C "${repository_root}" ls-files --others --exclude-standard -- v2)
[ -z "${untracked_v2}" ] || fail "untracked v2 source exists; commit or remove it before a production build"
source_revision=$(git -C "${repository_root}" rev-parse --verify HEAD)
[ "${#source_revision}" -eq 40 ] || fail "HEAD is not a canonical 40-character Git SHA"
case "${source_revision}" in *[!0-9a-f]*) fail "HEAD is not a canonical 40-character Git SHA" ;; esac

work_directory=$(mktemp -d "${TMPDIR:-/tmp}/agentserver-v2-production-images.XXXXXX")
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
    "${work_directory}/all-bin" \
    "${work_directory}/service-bin" \
    "${work_directory}/harness-bin" \
    "${work_directory}/managed-sandbox-bin" \
    "${work_directory}/service-context" \
    "${work_directory}/harness-context" \
    "${work_directory}/managed-sandbox-context"

build_root_binary() {
    source_command=$1
    output_name=$2
    printf '%s\n' "build-images.sh: compiling ${output_name} from cmd/${source_command}"
    (
        cd "${v2_root}"
        env CGO_ENABLED=0 GOOS=linux GOARCH="${goarch}" \
            go build -buildvcs=false -trimpath -ldflags='-s -w -buildid=' \
            -o "${work_directory}/all-bin/${output_name}" "./cmd/${source_command}"
    )
    chmod 0555 "${work_directory}/all-bin/${output_name}"
}

build_tae_binary() {
    source_command=$1
    output_name=$2
    printf '%s\n' "build-images.sh: compiling ${output_name} from providers/tae/cmd/${source_command}"
    (
        cd "${v2_root}/providers/tae"
        env CGO_ENABLED=0 GOOS=linux GOARCH="${goarch}" \
            go build -buildvcs=false -trimpath -ldflags='-s -w -buildid=' \
            -o "${work_directory}/all-bin/${output_name}" "./cmd/${source_command}"
    )
    chmod 0555 "${work_directory}/all-bin/${output_name}"
}

build_root_binary agentserver-core agentserver-core
build_root_binary harness-init agentserver-init
build_root_binary agentserver-probe agentserver-probe
build_root_binary platform-gateway platform-gateway
build_root_binary browser-gateway browser-gateway
build_root_binary executor-gateway executor-gateway
build_root_binary egress-authorizer egress-authorizer
build_root_binary llmproxy llmproxy
build_root_binary harness-final-exec harness-final-exec
build_root_binary harness-pool harness-pool
build_root_binary harness-worker harness-worker
build_root_binary tae-runtime agentserver-tae-runtime
build_tae_binary sandbox-gateway sandbox-gateway

for binary in agentserver-core agentserver-probe platform-gateway browser-gateway executor-gateway egress-authorizer llmproxy sandbox-gateway; do
    cp "${work_directory}/all-bin/${binary}" "${work_directory}/service-bin/${binary}"
done
for binary in agentserver-init agentserver-probe harness-final-exec harness-pool harness-worker; do
    cp "${work_directory}/all-bin/${binary}" "${work_directory}/harness-bin/${binary}"
done
cp "${work_directory}/all-bin/agentserver-tae-runtime" "${work_directory}/managed-sandbox-bin/agentserver-tae-runtime"
cp "${bkectl_artifact}" "${work_directory}/managed-sandbox-bin/bkectl"
cp "${lark_cli_artifact}" "${work_directory}/managed-sandbox-bin/lark-cli"
chmod 0555 \
    "${work_directory}/service-bin"/* \
    "${work_directory}/harness-bin"/* \
    "${work_directory}/managed-sandbox-bin"/*

printf '%s\n' "build-images.sh: compiling the host-side closed-world image verifier"
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
"${work_directory}/agentserver-image" prepare \
    --kind=harness \
    --platform="${platform}" \
    --source-revision="${source_revision}" \
    --binary-dir="${work_directory}/harness-bin" \
    --codex="${codex_artifact}" \
    --bwrap="${bwrap_artifact}" \
    --requirements="${v2_root}/packaging/stockruntime/requirements.toml" \
    --managed-skill="${managed_skill_artifact}" \
    --lark-skill="${lark_skill_artifact}" \
    --bkectl-skill-root="${bkectl_skill_root}" \
    --output="${work_directory}/harness-payload"
"${work_directory}/agentserver-image" prepare \
    --kind=managed-sandbox \
    --platform="${platform}" \
    --source-revision="${source_revision}" \
    --binary-dir="${work_directory}/managed-sandbox-bin" \
    --managed-skill="${managed_skill_artifact}" \
    --lark-skill="${lark_skill_artifact}" \
    --bkectl-skill-root="${bkectl_skill_root}" \
    --output="${work_directory}/managed-sandbox-payload"

# Both builders consume a regular archive so the already verified rootfs is
# the only mutable context input. BSD tar needs the explicit macOS metadata
# switches; GNU tar uses its own no-xattrs form. The exported OCI image is
# checked entry-by-entry against the external manifest after the build.
if [ "${engine}" = "apple-container" ]; then
	env COPYFILE_DISABLE=1 tar -cf "${work_directory}/service-context/rootfs.tar" \
		--format=ustar --no-xattrs --no-mac-metadata \
		-C "${work_directory}/service-payload/rootfs" .
	env COPYFILE_DISABLE=1 tar -cf "${work_directory}/harness-context/rootfs.tar" \
		--format=ustar --no-xattrs --no-mac-metadata \
		-C "${work_directory}/harness-payload/rootfs" .
	env COPYFILE_DISABLE=1 tar -cf "${work_directory}/managed-sandbox-context/rootfs.tar" \
		--format=ustar --no-xattrs --no-mac-metadata \
		-C "${work_directory}/managed-sandbox-payload/rootfs" .
else
	tar --format=ustar --no-xattrs -cf "${work_directory}/service-context/rootfs.tar" \
		-C "${work_directory}/service-payload/rootfs" .
	tar --format=ustar --no-xattrs -cf "${work_directory}/harness-context/rootfs.tar" \
		-C "${work_directory}/harness-payload/rootfs" .
	tar --format=ustar --no-xattrs -cf "${work_directory}/managed-sandbox-context/rootfs.tar" \
		-C "${work_directory}/managed-sandbox-payload/rootfs" .
fi
cp "${script_dir}/service.Containerfile" "${work_directory}/service-context/Dockerfile"
cp "${script_dir}/harness.Containerfile" "${work_directory}/harness-context/Dockerfile"
cp "${script_dir}/managed-sandbox.Containerfile" "${work_directory}/managed-sandbox-context/Dockerfile"
chmod 0444 \
    "${work_directory}/service-context/Dockerfile" \
    "${work_directory}/service-context/rootfs.tar" \
    "${work_directory}/harness-context/Dockerfile" \
    "${work_directory}/harness-context/rootfs.tar" \
    "${work_directory}/managed-sandbox-context/Dockerfile" \
    "${work_directory}/managed-sandbox-context/rootfs.tar"

printf '%s\n' "build-images.sh: building ${service_image}"
if [ "${engine}" = "apple-container" ]; then
	container build \
		--platform "${oci_platform}" \
		--progress plain \
		--no-cache \
		--build-arg "SOURCE_REVISION=${source_revision}" \
		--tag "${service_image}" \
		"${work_directory}/service-context"
else
	docker buildx build \
		--platform "${oci_platform}" \
		--progress plain \
		--no-cache \
		--provenance=false \
		--sbom=false \
		--build-arg "SOURCE_REVISION=${source_revision}" \
		--output "type=oci,dest=${work_directory}/service-image.oci.tar" \
		"${work_directory}/service-context"
fi

printf '%s\n' "build-images.sh: building ${harness_image}"
if [ "${engine}" = "apple-container" ]; then
	container build \
		--platform "${oci_platform}" \
		--progress plain \
		--no-cache \
		--build-arg "SOURCE_REVISION=${source_revision}" \
		--tag "${harness_image}" \
		"${work_directory}/harness-context"
else
	docker buildx build \
		--platform "${oci_platform}" \
		--progress plain \
		--no-cache \
		--provenance=false \
		--sbom=false \
		--build-arg "SOURCE_REVISION=${source_revision}" \
		--output "type=oci,dest=${work_directory}/harness-image.oci.tar" \
		"${work_directory}/harness-context"
fi

printf '%s\n' "build-images.sh: building ${managed_sandbox_image}"
if [ "${engine}" = "apple-container" ]; then
	container build \
		--platform "${oci_platform}" \
		--progress plain \
		--no-cache \
		--build-arg "SOURCE_REVISION=${source_revision}" \
		--tag "${managed_sandbox_image}" \
		"${work_directory}/managed-sandbox-context"
else
	docker buildx build \
		--platform "${oci_platform}" \
		--progress plain \
		--no-cache \
		--provenance=false \
		--sbom=false \
		--build-arg "SOURCE_REVISION=${source_revision}" \
		--output "type=oci,dest=${work_directory}/managed-sandbox-image.oci.tar" \
		"${work_directory}/managed-sandbox-context"
fi

verify_image() {
    kind=$1
    image=$2
    payload=$3
    archive=$4
    case "${kind}" in service|harness|managed-sandbox) ;; *) fail "internal unknown image kind" ;; esac
	if [ "${engine}" = "apple-container" ]; then
		container image save --platform "${oci_platform}" --output "${archive}" "${image}" >/dev/null
	fi
    chmod 0400 "${archive}"
    "${work_directory}/agentserver-image" verify-oci \
        --manifest="${payload}/image-manifest.json" \
        --archive="${archive}"
}

verify_image service "${service_image}" \
    "${work_directory}/service-payload" "${work_directory}/service-image.oci.tar"
verify_image harness "${harness_image}" \
    "${work_directory}/harness-payload" "${work_directory}/harness-image.oci.tar"
verify_image managed-sandbox "${managed_sandbox_image}" \
    "${work_directory}/managed-sandbox-payload" "${work_directory}/managed-sandbox-image.oci.tar"

mkdir -m 0700 "${output_directory}"
cp "${work_directory}/service-payload/image-manifest.json" "${output_directory}/service-image-manifest.json"
cp "${work_directory}/harness-payload/image-manifest.json" "${output_directory}/harness-image-manifest.json"
cp "${work_directory}/managed-sandbox-payload/image-manifest.json" "${output_directory}/managed-sandbox-image-manifest.json"
if [ "${engine}" = "apple-container" ]; then
	container image inspect "${service_image}" >"${output_directory}/service-image-inspect.json"
	container image inspect "${harness_image}" >"${output_directory}/harness-image-inspect.json"
	container image inspect "${managed_sandbox_image}" >"${output_directory}/managed-sandbox-image-inspect.json"
fi
cp "${work_directory}/service-image.oci.tar" "${output_directory}/service-image.oci.tar"
cp "${work_directory}/harness-image.oci.tar" "${output_directory}/harness-image.oci.tar"
cp "${work_directory}/managed-sandbox-image.oci.tar" "${output_directory}/managed-sandbox-image.oci.tar"
chmod 0444 "${output_directory}"/*

printf '%s\n' "build-images.sh: verified production images"
printf '%s\n' "  platform=${platform}"
printf '%s\n' "  engine=${engine}"
printf '%s\n' "  service=${service_image}"
printf '%s\n' "  harness=${harness_image}"
printf '%s\n' "  managed-sandbox=${managed_sandbox_image}"
printf '%s\n' "  evidence=${output_directory}"
