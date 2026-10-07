#!/usr/bin/env bash
set -euo pipefail
# This release path never calls TAE or compiles its SDK. Reuse the previously
# published CLI artifacts, not the legacy container entrypoint/root filesystem.
v2_root=$(cd "$(dirname "$0")/../.." && pwd)
: "${RELEASE_TAG:?}" "${HARNESS_BASE:?}" "${CLI_BASE:?}" "${RELEASE_DIRECTORY:?}"
test ! -e "$RELEASE_DIRECTORY"
mkdir -p "$RELEASE_DIRECTORY/cli/bkectl-skills" "$RELEASE_DIRECTORY/service/bin" "$RELEASE_DIRECTORY/harness/bin"
cli_container=$(docker create "$CLI_BASE" /usr/local/bin/bkectl)
harness_container=$(docker create "$HARNESS_BASE" /usr/local/bin/harness-pool)
trap 'docker rm "$cli_container" "$harness_container" >/dev/null' EXIT
docker cp "$cli_container:/usr/local/bin/bkectl" "$RELEASE_DIRECTORY/cli/bkectl"
docker cp "$cli_container:/usr/local/bin/lark-cli" "$RELEASE_DIRECTORY/cli/lark-cli"
# The image's pack directories are 0555. Restore directory modes only after
# their contents are extracted, so an unprivileged runner can populate them.
docker cp "$cli_container:/opt/agentserver/packs/bkectl" - | \
  tar -x --strip-components=1 --no-same-owner --no-same-permissions --delay-directory-restore -C "$RELEASE_DIRECTORY/cli/bkectl-skills"
docker cp "$harness_container:/opt/agentserver/runtime/bundle/codex-resources/bwrap" "$RELEASE_DIRECTORY/cli/bwrap"
export GOTOOLCHAIN=go1.26.5 CGO_ENABLED=0 GOOS=linux GOARCH=amd64
for binary in agentserver-core agentserver-probe platform-gateway browser-gateway executor-gateway egress-authorizer llmproxy; do
  go -C "$v2_root" build -trimpath -ldflags='-s -w' -o "$RELEASE_DIRECTORY/service/bin/$binary" "./cmd/$binary"
done
for binary in harness-pool harness-worker harness-init agentserver-probe; do
  destination=$binary
  if [ "$binary" = harness-init ]; then destination=agentserver-init; fi
  go -C "$v2_root" build -trimpath -ldflags='-s -w' -o "$RELEASE_DIRECTORY/harness/bin/$destination" "./cmd/$binary"
done
bash "$v2_root/deploy/production/build-kubernetes-images.sh" \
  --runtime-image="ghcr.io/agentserver/v2-k8s-runtime:$RELEASE_TAG" \
  --gateway-image="ghcr.io/agentserver/v2-k8s-gateway:$RELEASE_TAG" \
  --bkectl="$RELEASE_DIRECTORY/cli/bkectl" --lark-cli="$RELEASE_DIRECTORY/cli/lark-cli" \
  --bwrap="$RELEASE_DIRECTORY/cli/bwrap" --bkectl-skills="$RELEASE_DIRECTORY/cli/bkectl-skills" \
  --output-dir="$RELEASE_DIRECTORY/kubernetes" --engine=docker
for kind in service harness; do
  docker buildx build --platform linux/amd64 --load \
    --build-arg "SOURCE_REVISION=$GITHUB_SHA" --build-arg "HARNESS_BASE=$HARNESS_BASE" \
    -t "ghcr.io/agentserver/v2-$kind:$RELEASE_TAG" \
    -f "$v2_root/deploy/production/kubernetes-$kind.Containerfile" "$RELEASE_DIRECTORY/$kind"
done
docker buildx build --platform linux/amd64 --load \
  -t "ghcr.io/agentserver/v2-k8s-profile-installer:$RELEASE_TAG" \
  -f "$v2_root/deploy/production/security/profile-installer.Containerfile" "$v2_root/deploy/production/security"
docker run --rm --entrypoint /usr/local/bin/bkectl "ghcr.io/agentserver/v2-k8s-runtime:$RELEASE_TAG" --json version
docker run --rm --entrypoint /usr/local/bin/lark-cli "ghcr.io/agentserver/v2-k8s-runtime:$RELEASE_TAG" --version
for kind in service harness k8s-runtime k8s-gateway k8s-profile-installer; do
  image="ghcr.io/agentserver/v2-$kind:$RELEASE_TAG"
  docker push "$image"
  # Capture the registry's immutable address; this is artifact identification,
  # not a separate content hash or size verification gate.
  docker inspect --format '{{index .RepoDigests 0}}' "$image" >"$RELEASE_DIRECTORY/$kind.image"
done
