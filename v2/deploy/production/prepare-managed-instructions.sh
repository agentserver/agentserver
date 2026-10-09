#!/usr/bin/env bash
set -euo pipefail

source_directory=$(cd "$(dirname "$0")" && pwd)
pack_directory=${1:?usage: prepare-managed-instructions.sh /absolute/build/packs}
case "$pack_directory" in /*) ;; *) exit 2 ;; esac

# Keep the historical on-image paths for compatibility with older release
# documents. The managed-cli skill itself no longer imposes read-only policy.
for skill in managed-cli-readonly lark-readonly; do
  test ! -e "$pack_directory/$skill/SKILL.md"
done
for skill in managed-cli-readonly lark-readonly; do
  mkdir -p "$pack_directory/$skill"
  cp "$source_directory/$skill.SKILL.md" "$pack_directory/$skill/SKILL.md"
  chmod 0444 "$pack_directory/$skill/SKILL.md"
done
