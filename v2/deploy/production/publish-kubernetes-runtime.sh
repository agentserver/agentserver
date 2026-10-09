#!/usr/bin/env bash
set -euo pipefail
v2_root=$(cd "$(dirname "$0")/../.." && pwd)
: "${RELEASE_DIRECTORY:?}" "${GITHUB_SHA:?}"
test ! -e "$RELEASE_DIRECTORY"
mkdir -p "$RELEASE_DIRECTORY/runtime"
bash "$v2_root/deploy/production/prepare-managed-instructions.sh" "$RELEASE_DIRECTORY/runtime/packs"
for kind in service harness k8s-runtime k8s-gateway k8s-profile-installer; do
  jq -er --arg kind "$kind" '.images[$kind]' \
    "$v2_root/deploy/production/kubernetes-published-images.json" >"$RELEASE_DIRECTORY/$kind.image"
done
runtime_base=$(jq -er '.images["k8s-runtime"]' "$v2_root/deploy/production/kubernetes-published-images.json")
GOTOOLCHAIN=go1.26.5 CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go -C "$v2_root" build -trimpath -ldflags='-s -w' \
    -o "$RELEASE_DIRECTORY/runtime/agentserver-k8s-runtime" ./cmd/k8s-runtime
if [ "${CLEAN_BUILD_CACHE:-false}" = true ]; then
  go -C "$v2_root" clean -cache
fi
image="ghcr.io/agentserver/v2-k8s-runtime:stream-repair-$GITHUB_SHA"
docker buildx build --platform linux/amd64 --load --build-arg "RUNTIME_BASE=$runtime_base" \
  -t "$image" -f "$v2_root/deploy/production/kubernetes-runtime-repair.Containerfile" "$RELEASE_DIRECTORY/runtime"
docker run --rm --entrypoint /usr/local/bin/bkectl "$image" --json version
docker run --rm --entrypoint /usr/local/bin/lark-cli "$image" --version
docker run --rm --entrypoint /bin/sh "$image" -c 'printf "%s" "{\"ready\":true}" | jq -e .ready'
docker push "$image"
docker inspect --format '{{index .RepoDigests 0}}' "$image" >"$RELEASE_DIRECTORY/k8s-runtime.image"
