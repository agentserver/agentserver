#!/usr/bin/env bash
set -euo pipefail
# Install official release executables into the harness build context. Runtime
# identity metadata is checked in; no separate hash/size release gate is used.
v2_root=$(cd "$(dirname "$0")/../.." && pwd)
runtime_directory=${1:?runtime output directory required}
test ! -e "$runtime_directory"
manifest="$v2_root/packaging/stockruntime/runtime-manifest.json"
mkdir -p "$runtime_directory/bundle/bin" "$runtime_directory/bundle/codex-resources"
download_directory=$(mktemp -d)
for executable in codex bwrap; do
  if [ "$executable" = codex ]; then
    artifact='.artifacts["linux-amd64"].codex'
  else
    artifact='.artifacts["linux-amd64"].externalExecutables.bwrap'
  fi
  url=$(jq -er "$artifact.sourceUrl" "$manifest")
  destination=$(jq -er "$artifact.path" "$manifest")
  curl --fail --location --retry 3 "$url" --output "$download_directory/$executable.tar.gz"
  tar -xzf "$download_directory/$executable.tar.gz" -C "$download_directory"
  install -m 0555 "$download_directory/$executable-x86_64-unknown-linux-musl" "$runtime_directory/bundle/$destination"
  rm -- "$download_directory/$executable.tar.gz" "$download_directory/$executable-x86_64-unknown-linux-musl"
done
rmdir "$download_directory"
install -m 0444 "$manifest" "$runtime_directory/runtime-manifest.json"
