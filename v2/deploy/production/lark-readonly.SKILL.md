---
name: lark-readonly
version: 1.0.0
description: "Read Lark/Feishu Wiki and Docx documents through the managed lark-cli runtime."
metadata:
  requires:
    bins: ["lark-cli"]
  cliHelp: "lark-cli skills read lark-doc;lark-cli docs +fetch --help"
---

# Managed Lark document reads

Use this skill only to read a Lark/Feishu Wiki or Docx document when the user asks for document lookup, retrieval, or summarization.

The managed executor supplies an operation-scoped user identity to `lark-cli`. Never initialize, inspect, refresh, export, or modify authentication. In particular, do not run `auth`, `config`, `profile`, `update`, `whoami`, or commands that inspect process environment or credential files.

Before choosing `docs +fetch` flags, read the version-matched guidance embedded in the pinned CLI:

```text
lark-cli skills read lark-doc references/lark-doc-fetch.md
```

Then invoke the CLI directly, without `sh -c`, `bash -c`, pipelines, redirects, or another executable. The normal full-document form is:

```text
lark-cli docs +fetch --as user --doc <document-url-or-token> --scope full --detail simple --format json
```

For large documents, follow the embedded guidance and prefer `outline`, `section`, `range`, or `keyword` scope so the result remains bounded. Treat document URLs and tokens as opaque values; do not rewrite them.

This pack is read-only. Do not use raw `api`, any write/high-risk-write command, or any domain other than the embedded `skills read` command and `docs +fetch`. If the requested operation cannot be completed with those commands, stop and explain that the managed Lark pack does not authorize it. Do not try another network client or bypass the managed egress policy.
