#!/usr/bin/env bash
set -euo pipefail

# Prepare artifacts independently from the legacy TAE image pipeline. No hash
# or artifact-size release gates are introduced here. CLI versions are chosen
# by the caller/CI; runtime correctness is checked by the canary, not filename.
repo_root=$(cd "$(dirname "$0")/../.." && pwd)
runtime_image=""
gateway_image=""
bkectl_binary=""
lark_binary=""
bwrap_binary=""
bkectl_skills=""
output_directory=""
engine="docker"
usage() {
  printf '%s\n' 'build-kubernetes-images.sh --runtime-image=REF --gateway-image=REF --bkectl=/path --lark-cli=/path --bwrap=/path --bkectl-skills=/path --output-dir=/new/path [--engine=docker|container]'
}
for argument in "$@"; do
  case "$argument" in
    --runtime-image=*) runtime_image=${argument#*=} ;;
    --gateway-image=*) gateway_image=${argument#*=} ;;
    --bkectl=*) bkectl_binary=${argument#*=} ;;
    --lark-cli=*) lark_binary=${argument#*=} ;;
    --bwrap=*) bwrap_binary=${argument#*=} ;;
    --bkectl-skills=*) bkectl_skills=${argument#*=} ;;
    --output-dir=*) output_directory=${argument#*=} ;;
    --engine=*) engine=${argument#*=} ;;
    *) usage >&2; exit 2 ;;
  esac
done
for artifact in "$bkectl_binary" "$lark_binary" "$bwrap_binary"; do
  case "$artifact" in /*) ;; *) usage >&2; exit 2 ;; esac
  test -f "$artifact"
done
case "$output_directory" in /*) ;; *) usage >&2; exit 2 ;; esac
test -n "$runtime_image" && test -n "$gateway_image"
test -f "$bkectl_skills/SKILL.md"
test ! -e "$output_directory"
case "$engine" in docker|container) ;; *) usage >&2; exit 2 ;; esac

mkdir -p "$output_directory/runtime/packs/bkectl" "$output_directory/gateway"
GOTOOLCHAIN=go1.26.5 CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go -C "$repo_root" build -trimpath -o "$output_directory/runtime/agentserver-k8s-runtime" ./cmd/k8s-runtime
GOTOOLCHAIN=go1.26.5 CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go -C "$repo_root/providers/kubernetes" build -trimpath -o "$output_directory/gateway/sandbox-gateway-k8s" ./cmd/sandbox-gateway
GOTOOLCHAIN=go1.26.5 CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go -C "$repo_root" build -trimpath -o "$output_directory/gateway/agentserver-probe" ./cmd/agentserver-probe
cp "$bkectl_binary" "$output_directory/runtime/bkectl"
cp "$lark_binary" "$output_directory/runtime/lark-cli"
cp "$bwrap_binary" "$output_directory/runtime/bwrap"
cp -R "$bkectl_skills/." "$output_directory/runtime/packs/bkectl/"
mkdir -p "$output_directory/runtime/packs/managed-cli-readonly" "$output_directory/runtime/packs/lark-readonly"
cp "$repo_root/deploy/production/managed-cli-readonly.SKILL.md" "$output_directory/runtime/packs/managed-cli-readonly/SKILL.md"
cp "$repo_root/deploy/production/lark-readonly.SKILL.md" "$output_directory/runtime/packs/lark-readonly/SKILL.md"
chmod 0555 "$output_directory/runtime/agentserver-k8s-runtime" "$output_directory/runtime/bwrap" "$output_directory/runtime/bkectl" "$output_directory/runtime/lark-cli" "$output_directory/gateway/sandbox-gateway-k8s"

if [ "$engine" = docker ]; then
  docker buildx build --platform linux/amd64 --load -t "$runtime_image" -f "$repo_root/deploy/production/kubernetes-runtime.Containerfile" "$output_directory/runtime"
  docker buildx build --platform linux/amd64 --load -t "$gateway_image" -f "$repo_root/deploy/production/kubernetes-gateway.Containerfile" "$output_directory/gateway"
else
  container build --arch amd64 -t "$runtime_image" -f "$repo_root/deploy/production/kubernetes-runtime.Containerfile" "$output_directory/runtime"
  container build --arch amd64 -t "$gateway_image" -f "$repo_root/deploy/production/kubernetes-gateway.Containerfile" "$output_directory/gateway"
fi
