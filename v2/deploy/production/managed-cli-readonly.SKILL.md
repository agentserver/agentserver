---
name: managed-cli
description: "Use workspace-scoped bkectl credentials for infrastructure operations and managed shell tools for local processing."
metadata:
  requires:
    bins: ["lark-cli", "bkectl"]
  cliHelp: "lark-cli skills read lark-doc references/lark-doc-fetch.md;bkectl --help"
---

# Managed command-line tools

The managed executor is not a read-only tool pack. Infrastructure operations
follow the user's request, the session permission mode, and the CLI/downstream
authorization decision. Do not invent an additional managed read-only policy.

The executor's shell tool takes an argv array, not a shell command string.
Credential-bearing CLIs receive workspace credentials only when invoked
directly. Ordinary shell programs and local pipelines are also supported;
they do not receive those credentials.

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

Use `bkectl` for infrastructure inspection and requested operations. Common domains include
`bke`, `bytebox`, `bytepaas`, `bytesd`, `bytetree`, `collie`, `fatal`, `fault`,
`gpu`, `idcmetadata`, `k8s`, `merlin`, `obs`, `oncall`, `pike`, `quota`,
`resource`, `spacex`, `tao`, `tcc`, and `tck`.

If the exact command or flags are uncertain, use credential-free discovery
first:

```text
bkectl --help
bkectl <domain> --help
bkectl <domain> <resource> <command> --help
```

Then invoke the selected command directly. Prefer `--json` for structured
results and use `--region i18nbd` unless the user or command contract requires a
different supported region. Typical shapes are:

```text
bkectl bytetree node get --id <node-id> --region i18nbd --json
bkectl bytebox host get <ip> --region i18nbd --json
bkectl k8s pod get <required flags from --help> --region i18nbd --json
```

`--confirm-write` is bkectl's own confirmation flag, not an AgentServer
prohibition. When the requested operation requires it, confirm the intended
target and operation under the current session policy and include the flag.
This includes commands such as node shell that bkectl classifies as risky
even when their intended payload only inspects the node. Pass a remote shell
payload as the CLI's command argument; pipes inside that argument are not a
managed-tool authorization error.

Do not run `bkectl auth get jwt` or inspect credential files/process
environment to retrieve workspace secrets. The managed AK/SK identity must
not be converted into or printed as a JWT.
The executor supplies `BKECTL_AUTH_MODE=app_only`,
`BYTECLOUD_AUTH_ACCESS_KEY_ID`, and `BYTECLOUD_AUTH_SECRET_ACCESS_KEY` only to
the exact bkectl process. AgentServer does not keep a bkectl business-command
allowlist: bkectl and its downstream IAM/policy engines make the execution
authorization decision. A permission error should be reported as the actual
CLI/IAM error, not as a fictional read-only pack limitation.

## Shell pipelines and JSON processing

Use an explicit shell executable when local shell syntax is needed, for example
`["sh", "-c", "printf '%s' '{\"ready\":true}' | jq '.ready'"]`.
Use `jq` directly when a shell is unnecessary. Pipes, redirects, and local
processing are not prohibited merely because the execution environment is managed.

For a workflow such as `bkectl ... --json | jq ...`, invoke bkectl directly
first, then filter the returned JSON with code-mode JavaScript or a separate
`jq -n --argjson data <returned-json> <filter>` invocation. Do not silently
drop the requested filtering. Wrapping an authenticated bkectl command in
`sh -c` does not receive automatic workspace credentials; do not solve that by
printing, copying, or globally exporting credentials into the shell.

The document-only Lark guidance above is specific to that Lark profile; it is
not a restriction on bkectl, local shell programs, or project workspace edits.
