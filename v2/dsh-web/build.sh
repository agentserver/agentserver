#!/usr/bin/env bash
set -euo pipefail
repo_root=$(cd "$(dirname "$0")/../.." && pwd)
source_root="$repo_root/third_party/deepseek-harness"
output_root="$repo_root/v2/dsh-web/dist"
if [ ! -f "$source_root/package.json" ]; then
  printf '%s\n' 'Initialize DSH source: git submodule update --init third_party/deepseek-harness' >&2
  exit 1
fi
build_work=$(mktemp -d "${TMPDIR:-/tmp}/agentserver-dsh-build.XXXXXX")
export CI=true LEFTHOOK=0 DSH_CLIENT_TITLE=agentserver
# Disable hook installation only during dependency/build work. Git commits and
# pushes still run their normal hooks. The source checkout is the pinned gitlink.
npm exec --yes --package=pnpm@11.7.0 -- pnpm --dir "$source_root" install --frozen-lockfile
node "$source_root/node_modules/pnpm/bin/pnpm.cjs" --dir "$source_root" run build
node "$repo_root/v2/dsh-web/export.mjs" "$source_root" "$build_work/dist"
mkdir -p "$output_root"
rsync -a --delete --exclude=.gitkeep "$build_work/dist/" "$output_root/"
printf 'DSH frontend built from %s\n' "$(git -C "$source_root" rev-parse HEAD)"
