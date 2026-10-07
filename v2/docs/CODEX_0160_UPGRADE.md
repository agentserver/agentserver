# Stock Codex 0.160.1 upgrade

SG deployed on 2026-10-07, Helm revision 163, chart
`0.1.0-config.d1af48350c9e6`, application source `95df94fb` and successful
GHCR publication run `37643547331`. Both live Harness replicas report
`codex-cli 0.160.1`; all Deployments are Ready. The database remains at schema
35 and retains all 103 pre-upgrade checkpoints. Public DSH and platform pages
are available. Logged-in real-model conversation acceptance still requires a
user message; readiness is not a substitute for that check.

The first publication runner was evicted for node ephemeral-storage pressure,
not OOM. The retry reused the completed frontend artifact from run
`37640754326`, removed its disposable test/build caches before Docker packaging,
and succeeded on another SG node. No shared node resources were deleted.

The Harness now packages official stable Codex 0.160.1 (upstream commit
`d27764b82f7118f674371e6d6e76271d9d606edb`). This is a runtime upgrade, not a
model migration: workspace/session model selections remain unchanged.

## Session continuity

Native rollout checkpoints from the previously deployed 0.146.0 production
manifest may resume on the new manifest with checkpoint allowlist version 1.
Only this exact forward transition is accepted. The source checkpoint is
authenticated against its original identity; new checkpoints record 0.160.1.
History, catalog, sequence numbers and cursor positions are not rewritten.
Downgrading new checkpoints to 0.146.0 is not supported.

`TestAppServerA09Upgrade0146To0160Checkpoint` uses both stock executables,
retires the original CODEX_HOME, restores only the rollout, and proves that
prior messages/results/catalog survive without replaying completed tool calls.
Other live probes cover exact tool exposure, interruption, model capability
authentication, secret exclusion, readable Chinese output and temporary title
generation. Models are local scripted servers; no live model credentials are
required. The release workflow repeats these probes on native SG Linux.

## Packaging and rollout

DSH stays source-built from its pinned fork, with no frontend protocol changes.
The service and Harness publish directly to GHCR. Harness packaging overlays
both the new stock executable/resource bundle and its identity manifest onto
the qualified base. No new digest/size release verification gates are added.
The Kubernetes executor, gateway, bkectl, lark-cli, node security profiles and
default RuntimeClass are unchanged. Managed environment compatibility metadata
describes the compatible Harness, not an embedded Codex executable in the Go
Kubernetes runtime.

Release preparation explicitly requests `--upgrade-codex`; ordinary deployment
config parsing does not silently upgrade old manifests. The Harness Deployment
uses Recreate to avoid old pools attempting to read new checkpoints during a
rolling overlap. Deploy after active attempts have drained; expect a brief
worker availability gap. No database schema migration is needed.

After sessions write new checkpoints, a rollback must retain a runtime that
can read those checkpoints. Do not blindly restore the old Harness image.

## Readable tool results

Executor transport stays byte-safe (base64 chunks). The Harness formats only
known executor shell/read-file results as metadata plus readable text before
passing them to Codex. DSH also formats historical complete transport results
on read. Binary bytes remain explicitly identified, with a small hex preview;
truncation, exit status, output completeness and file offsets remain visible.
