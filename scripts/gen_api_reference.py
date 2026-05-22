#!/usr/bin/env python3
"""Generate per-tag Markdown API reference from docs/api/openapi.yaml.

Output: docs/api/reference/<tag-slug>.md and docs/api/reference/README.md.

Only tags listed in EXTERNAL_TAGS are rendered (the public developer
surface). Admin and untagged ("Misc") endpoints are intentionally
excluded; consult the raw spec for those.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parent.parent
SPEC_PATH = ROOT / "docs" / "api" / "openapi.yaml"
DEFAULT_OUT_DIR = ROOT / "docs" / "api" / "reference"

# Tags rendered into the public developer reference. Order here is the
# order they appear in the index. Each entry maps a tag name (as used
# in the OpenAPI spec) to the Markdown filename.
EXTERNAL_TAGS: list[tuple[str, str]] = [
    ("Auth", "auth.md"),
    ("Workspaces", "workspaces.md"),
    ("Workspace API Keys", "workspace-api-keys.md"),
    ("Sandboxes", "sandboxes.md"),
    ("Agent", "agent.md"),
    ("Codex Tokens", "codex-tokens.md"),
    ("Codex Browser Sessions", "codex-browser-sessions.md"),
    ("IM Channels", "im-channels.md"),
]

METHOD_ORDER = ["get", "post", "put", "patch", "delete"]


def slugify(text: str) -> str:
    s = re.sub(r"[^a-zA-Z0-9]+", "-", text).strip("-").lower()
    return s or "x"


def resolve_ref(spec: dict, ref: str) -> dict:
    assert ref.startswith("#/"), ref
    node = spec
    for part in ref[2:].split("/"):
        node = node[part]
    return node


def schema_summary(spec: dict, schema: dict, depth: int = 0) -> str:
    """Render a schema as a compact YAML-ish block for humans."""
    if depth > 4:
        return "..."
    if "$ref" in schema:
        name = schema["$ref"].rsplit("/", 1)[-1]
        resolved = resolve_ref(spec, schema["$ref"])
        # Inline named schemas once; link by name in heading later.
        return schema_summary(spec, resolved, depth) + f"  # {name}"
    t = schema.get("type")
    if t == "array":
        inner = schema_summary(spec, schema.get("items", {}), depth + 1)
        # Indent the inner block.
        inner_indented = "\n".join("  " + line for line in inner.splitlines())
        return f"[\n{inner_indented}\n]"
    if t == "object" or "properties" in schema:
        props = schema.get("properties", {})
        required = set(schema.get("required", []))
        if not props:
            extra = schema.get("additionalProperties")
            if isinstance(extra, dict):
                inner = schema_summary(spec, extra, depth + 1)
                return f"{{ <key>: {inner} }}"
            return "{}"
        lines = []
        for name, sub in props.items():
            marker = "" if name in required else "?"
            lines.append(f"{name}{marker}: {field_type(spec, sub)}")
        return "{\n" + "\n".join("  " + line for line in lines) + "\n}"
    return field_type(spec, schema)


def field_type(spec: dict, schema: dict) -> str:
    if "$ref" in schema:
        return schema["$ref"].rsplit("/", 1)[-1]
    t = schema.get("type")
    fmt = schema.get("format")
    if t == "array":
        return f"[]{field_type(spec, schema.get('items', {}))}"
    if t == "object":
        return "object"
    if fmt:
        return f"{t}<{fmt}>"
    if "enum" in schema:
        return f"enum({'|'.join(map(str, schema['enum']))})"
    return t or "any"


def render_params(params: list[dict]) -> str:
    if not params:
        return ""
    by_loc: dict[str, list[dict]] = {}
    for p in params:
        by_loc.setdefault(p.get("in", "query"), []).append(p)
    out = []
    for loc in ("path", "query", "header"):
        ps = by_loc.get(loc, [])
        if not ps:
            continue
        out.append(f"\n**{loc.capitalize()} parameters**\n")
        out.append("| Name | Type | Required | Description |")
        out.append("|------|------|----------|-------------|")
        for p in ps:
            req = "yes" if p.get("required") else "no"
            t = field_type({}, p.get("schema", {}))
            desc = (p.get("description") or "").replace("\n", " ").replace("|", "\\|")
            out.append(f"| `{p['name']}` | `{t}` | {req} | {desc} |")
    return "\n".join(out) + "\n"


def render_request_body(spec: dict, op: dict) -> str:
    body = op.get("requestBody")
    if not body:
        return ""
    if "$ref" in body:
        body = resolve_ref(spec, body["$ref"])
    out = ["\n**Request body**\n"]
    content = body.get("content", {})
    for media, mc in content.items():
        out.append(f"Content-Type: `{media}`\n")
        schema = mc.get("schema", {})
        if "$ref" in schema:
            name = schema["$ref"].rsplit("/", 1)[-1]
            out.append(f"Schema: [`{name}`](#schema-{slugify(name)})\n")
            resolved = resolve_ref(spec, schema["$ref"])
            out.append("```yaml")
            out.append(schema_summary(spec, resolved))
            out.append("```")
        else:
            out.append("```yaml")
            out.append(schema_summary(spec, schema))
            out.append("```")
    return "\n".join(out) + "\n"


def render_responses(spec: dict, op: dict) -> str:
    responses = op.get("responses", {})
    out = ["\n**Responses**\n"]
    out.append("| Status | Description | Schema |")
    out.append("|--------|-------------|--------|")
    for code in sorted(responses.keys()):
        resp = responses[code]
        desc = (resp.get("description") or "").replace("\n", " ").replace("|", "\\|")
        schema_label = "—"
        content = resp.get("content") or {}
        for media, mc in content.items():
            s = mc.get("schema", {})
            if "$ref" in s:
                name = s["$ref"].rsplit("/", 1)[-1]
                schema_label = f"[`{name}`](#schema-{slugify(name)})"
            elif s.get("type") == "array" and "$ref" in s.get("items", {}):
                name = s["items"]["$ref"].rsplit("/", 1)[-1]
                schema_label = f"array of [`{name}`](#schema-{slugify(name)})"
            else:
                schema_label = f"`{field_type(spec, s)}`"
            break
        out.append(f"| `{code}` | {desc} | {schema_label} |")
    return "\n".join(out) + "\n"


def render_operation(spec: dict, path: str, method: str, op: dict) -> str:
    summary = op.get("summary") or f"{method.upper()} {path}"
    anchor = slugify(f"{method}-{path}")
    parts = [f"### `{method.upper()} {path}` {{#op-{anchor}}}", ""]
    parts.append(summary)
    if op.get("description"):
        parts.append("")
        parts.append(op["description"])
    sec = op.get("security")
    if sec is not None:
        if not sec:
            parts.append("\n**Auth:** none\n")
        else:
            schemes = []
            for s in sec:
                schemes.extend(s.keys())
            parts.append(f"\n**Auth:** {', '.join(f'`{x}`' for x in schemes)}\n")
    parts.append(render_params(op.get("parameters", [])))
    parts.append(render_request_body(spec, op))
    parts.append(render_responses(spec, op))
    return "\n".join(p for p in parts if p != "")


def collect_referenced_schemas(spec: dict, op: dict, acc: set[str]) -> None:
    def walk(node):
        if isinstance(node, dict):
            for k, v in node.items():
                if k == "$ref" and isinstance(v, str):
                    if v.startswith("#/components/schemas/"):
                        name = v.rsplit("/", 1)[-1]
                        if name not in acc:
                            acc.add(name)
                            walk(spec["components"]["schemas"][name])
                    elif v.startswith("#/components/"):
                        # Resolve requestBodies, responses, parameters refs and
                        # keep walking through them so nested schema refs get
                        # picked up.
                        walk(resolve_ref(spec, v))
                else:
                    walk(v)
        elif isinstance(node, list):
            for v in node:
                walk(v)
    walk(op)


def render_schema(spec: dict, name: str, schema: dict) -> str:
    parts = [f"### `{name}` {{#schema-{slugify(name)}}}", ""]
    if schema.get("description"):
        parts.append(schema["description"])
        parts.append("")
    parts.append("```yaml")
    parts.append(schema_summary(spec, schema))
    parts.append("```")
    return "\n".join(parts)


def render_tag_page(spec: dict, tag: str, ops: list[tuple[str, str, dict]]) -> str:
    lines = [f"# {tag}", ""]
    lines.append(f"Endpoints under the `{tag}` tag. Auto-generated from "
                 f"[`docs/api/openapi.yaml`](../openapi.yaml) — do not edit by hand.")
    lines.append("")
    lines.append("> Run `make api-docs` after changing handler annotations to regenerate this file.")
    lines.append("")

    lines.append("## Operations")
    lines.append("")
    lines.append("| Method | Path | Summary |")
    lines.append("|--------|------|---------|")
    for path, method, op in ops:
        anchor = slugify(f"{method}-{path}")
        summary = (op.get("summary") or "").replace("|", "\\|")
        lines.append(f"| `{method.upper()}` | [`{path}`](#op-{anchor}) | {summary} |")
    lines.append("")

    for path, method, op in ops:
        lines.append(render_operation(spec, path, method, op))
        lines.append("")

    referenced: set[str] = set()
    for _, _, op in ops:
        collect_referenced_schemas(spec, op, referenced)
    if referenced:
        lines.append("## Schemas")
        lines.append("")
        for name in sorted(referenced):
            schema = spec["components"]["schemas"].get(name)
            if schema is None:
                continue
            lines.append(render_schema(spec, name, schema))
            lines.append("")

    return "\n".join(lines).rstrip() + "\n"


def render_index(spec: dict, tag_ops: dict[str, list]) -> str:
    info = spec.get("info", {})
    title = info.get("title", "API")
    version = info.get("version", "")
    lines = [f"# {title} — Developer Reference", ""]
    if version:
        lines.append(f"Spec version: `{version}`")
        lines.append("")
    lines.append(
        "This reference covers the **external developer surface** of agentserver: "
        "the endpoints an app or custom agent integrator will use. Admin and "
        "internal endpoints are intentionally excluded — see the raw "
        "[`openapi.yaml`](../openapi.yaml) for the full surface.")
    lines.append("")
    lines.append("## Conventions")
    lines.append("")
    lines.append("- **Base URL.** All paths are relative to your agentserver host, e.g. `https://agent.example.com`.")
    lines.append("- **Auth schemes.** Three schemes are used across the API:")
    lines.append("  - `CookieAuth` — browser session cookie set by `POST /api/auth/login` or the OIDC callbacks.")
    lines.append("  - `BearerAuth` — `Authorization: Bearer <token>` with one of: OAuth access token (Device Flow), `proxy_token` returned from `POST /api/agent/register`, or a workspace API key (`wak_*`).")
    lines.append("  - The auth column on each endpoint shows what is accepted; many endpoints accept either cookie or bearer.")
    lines.append("- **Errors.** Non-2xx responses are plain JSON strings unless documented otherwise. The status code is the source of truth — `400` validation, `401` not authenticated, `403` not authorized, `404` not found, `409` conflict, `500` unexpected.")
    lines.append("- **IDs.** UUIDs are returned as canonical 36-char hex with dashes. Sandbox `short_id`s are 16 chars, used in proxy subdomains.")
    lines.append("- **Timestamps.** ISO-8601 UTC (RFC 3339).")
    lines.append("")
    lines.append("## Sections")
    lines.append("")
    lines.append("| Tag | Endpoints | Page |")
    lines.append("|-----|-----------|------|")
    for tag, filename in EXTERNAL_TAGS:
        ops = tag_ops.get(tag, [])
        if not ops:
            continue
        lines.append(f"| {tag} | {len(ops)} | [`{filename}`]({filename}) |")
    lines.append("")
    lines.append("## Related docs")
    lines.append("")
    lines.append("- [`../../developer/quickstart.md`](../../developer/quickstart.md) — build a custom agent in 5 minutes.")
    lines.append("- [`../../developer/protocol.md`](../../developer/protocol.md) — full custom-agent tunnel protocol (WebSocket + yamux).")
    lines.append("- [`../mobile-integration.md`](../mobile-integration.md) — mobile/IM integration notes.")
    lines.append("- [`../openapi.yaml`](../openapi.yaml) — machine-readable source of truth.")
    return "\n".join(lines) + "\n"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", "-o", type=Path, default=DEFAULT_OUT_DIR,
                        help="Output directory (default: docs/api/reference)")
    parser.add_argument("--spec", type=Path, default=SPEC_PATH,
                        help="OpenAPI spec to read (default: docs/api/openapi.yaml)")
    args = parser.parse_args()

    spec = yaml.safe_load(args.spec.read_text())
    paths = spec.get("paths", {})

    # Index: tag -> [(path, method, op)]
    tag_ops: dict[str, list[tuple[str, str, dict]]] = {t: [] for t, _ in EXTERNAL_TAGS}
    wanted = {t for t, _ in EXTERNAL_TAGS}

    for path in sorted(paths.keys()):
        methods = paths[path]
        for method in METHOD_ORDER:
            op = methods.get(method)
            if not op:
                continue
            for tag in op.get("tags", []):
                if tag in wanted:
                    tag_ops[tag].append((path, method, op))

    args.output.mkdir(parents=True, exist_ok=True)

    for tag, filename in EXTERNAL_TAGS:
        ops = tag_ops[tag]
        if not ops:
            print(f"warning: tag '{tag}' has no operations — skipping", file=sys.stderr)
            continue
        out_path = args.output / filename
        out_path.write_text(render_tag_page(spec, tag, ops))
        print(f"wrote {out_path}")

    index_path = args.output / "README.md"
    index_path.write_text(render_index(spec, tag_ops))
    print(f"wrote {index_path}")

    return 0


if __name__ == "__main__":
    sys.exit(main())
