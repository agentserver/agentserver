# Stock model metadata

`upstream-0.160.1.json` is the unmodified file
`codex-rs/models-manager/models.json` from `openai/codex`, tag `rust-v0.160.1`
(commit `d27764b82f7118f674371e6d6e76271d9d606edb`), licensed Apache-2.0.

AgentServer supplies a runtime copy with `tool_mode` set to `direct` and
`use_responses_lite` set to `false` to retain standard Responses tool fields.
The worker's frozen dynamic executor catalog remains the tool authority;
model defaults must not enable a different execution path. Model names,
context windows, reasoning support and prompt metadata are otherwise intact.
Unknown custom model aliases continue to use Codex's normal fallback metadata.

This is source configuration data, not compiled frontend output or a patched
Codex executable. It is installed in each fresh CODEX_HOME before app-server
starts, and is not restored from the session checkpoint.
