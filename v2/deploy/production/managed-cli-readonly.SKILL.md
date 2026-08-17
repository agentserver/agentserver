---
name: managed-cli-readonly
version: 1.0.0
description: "Read Lark documents and ByteCloud/BKE infrastructure through the managed TAE command-line runtime."
metadata:
  requires:
    bins: ["lark-cli", "bkectl"]
  cliHelp: "lark-cli skills read lark-doc references/lark-doc-fetch.md;bkectl --help"
---

# Managed read-only command-line tools

The managed executor provides a workspace-scoped user identity only to an
approved command. Invoke a CLI directly with an argv array. Never use `sh -c`,
`bash -c`, pipelines, redirects, command substitution, another executable, or
commands that inspect process environment, `/proc`, credential files, or local
authentication state.

## Lark and Feishu documents

Use `lark-cli` only to read a Lark/Feishu Wiki or Docx document when the user
asks for document lookup, retrieval, or summarization. Do not run `auth`,
`config`, `profile`, `update`, `whoami`, raw `api`, or write commands.

Before selecting fetch flags, read the version-matched guidance embedded in the
pinned CLI:

```text
lark-cli skills read lark-doc references/lark-doc-fetch.md
```

The normal full-document form is:

```text
lark-cli docs +fetch --as user --doc <document-url-or-token> --scope full --detail simple --format json
```

For large documents, follow the embedded guidance and prefer `outline`,
`section`, `range`, or `keyword` scope. Treat document URLs and tokens as opaque
values.

## ByteCloud, BKE, Kubernetes, machines, quota, and SRE resources

Use `bkectl` for read-only infrastructure inspection. Common domains include
`bke`, `bytebox`, `bytepaas`, `bytesd`, `bytetree`, `collie`, `fatal`, `fault`,
`gpu`, `idcmetadata`, `k8s`, `merlin`, `obs`, `oncall`, `pike`, `quota`,
`resource`, `spacex`, `tao`, `tcc`, and `tck`.

If the exact command or flags are uncertain, use credential-free discovery
first:

```text
bkectl --help
bkectl <domain> --help
bkectl <domain> <resource> <read-command> --help
```

Then invoke one read-only leaf command directly. Prefer `--json` for structured
results and use `--region i18nbd` unless the user or command contract requires a
different supported region. Typical shapes are:

```text
bkectl bytetree node get --id <node-id> --region i18nbd --json
bkectl bytebox host get <ip> --region i18nbd --json
bkectl k8s pod get <required flags from --help> --region i18nbd --json
```

Never run any `bkectl auth` command, including `auth get jwt`; the injected JWT
must not be printed. Never run installation, login, logout, update, create,
delete, mutate, repair, shell, exec, block, unblock, or another write/risky
operation. Never add `--debug` or `--confirm-write`. AgentServer does not keep
a bkectl command allowlist: bkectl and its downstream IAM/policy engines make
the execution authorization decision.

If a requested operation is outside these read-only capabilities, stop and
explain that the managed pack does not support it. Do not try another network
client or bypass the managed execution boundary.
