#!/usr/bin/env bash
set -euo pipefail
# Application repair; optionally rebuild harness, retaining qualified sandbox
# runtime, gateway, Codex bundle and node profiles.
v2_root=$(cd "$(dirname "$0")/../.." && pwd)
: "${RELEASE_DIRECTORY:?}" "${GITHUB_SHA:?}"
test ! -e "$RELEASE_DIRECTORY"
test -f "$v2_root/dsh-web/dist/plugin-resources.json" || { printf '%s\n' 'Build the pinned DSH submodule with bash v2/dsh-web/build.sh first' >&2; exit 1; }
mkdir -p "$RELEASE_DIRECTORY/service/bin"
for kind in harness k8s-runtime k8s-gateway k8s-profile-installer; do
  jq -er --arg kind "$kind" '.images[$kind]' \
    "$v2_root/deploy/production/kubernetes-published-images.json" >"$RELEASE_DIRECTORY/$kind.image"
done
export GOTOOLCHAIN=go1.26.5 CGO_ENABLED=0 GOOS=linux GOARCH=amd64
for binary in agentserver-core agentserver-probe platform-gateway browser-gateway executor-gateway egress-authorizer llmproxy; do
  go -C "$v2_root" build -trimpath -ldflags='-s -w' -o "$RELEASE_DIRECTORY/service/bin/$binary" "./cmd/$binary"
done
image="ghcr.io/agentserver/v2-service:k8s-service-$GITHUB_SHA"
docker buildx build --platform linux/amd64 --load --build-arg "SOURCE_REVISION=$GITHUB_SHA" \
  -t "$image" -f "$v2_root/deploy/production/kubernetes-service.Containerfile" "$RELEASE_DIRECTORY/service"
docker push "$image"
docker inspect --format '{{index .RepoDigests 0}}' "$image" >"$RELEASE_DIRECTORY/service.image"

if [ "${PUBLISH_HARNESS:-false}" = true ]; then
  mkdir -p "$RELEASE_DIRECTORY/harness/bin"
  harness_base=$(jq -er '.images.harness' "$v2_root/deploy/production/kubernetes-published-images.json")
  for binary in harness-pool harness-worker harness-init agentserver-probe; do
    destination=$binary
    if [ "$binary" = harness-init ]; then destination=agentserver-init; fi
    go -C "$v2_root" build -trimpath -ldflags='-s -w' -o "$RELEASE_DIRECTORY/harness/bin/$destination" "./cmd/$binary"
  done
  harness_image="ghcr.io/agentserver/v2-harness:k8s-harness-$GITHUB_SHA"
  docker buildx build --platform linux/amd64 --load \
    --build-arg "SOURCE_REVISION=$GITHUB_SHA" --build-arg "HARNESS_BASE=$harness_base" \
    -t "$harness_image" -f "$v2_root/deploy/production/kubernetes-harness.Containerfile" "$RELEASE_DIRECTORY/harness"
  docker push "$harness_image"
  docker inspect --format '{{index .RepoDigests 0}}' "$harness_image" >"$RELEASE_DIRECTORY/harness.image"
fi
