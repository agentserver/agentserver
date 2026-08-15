// Package mcpcontract defines the model-visible executor MCP catalog. The
// catalog is intentionally independent from the MCP transport implementation
// so the harness can freeze it before executor-gateway handlers exist.
package mcpcontract

import "encoding/json"

const (
	Version              = "executor-mcp/1.1"
	Namespace            = "executor"
	NamespaceDescription = "Deterministic executor tools."
)

const (
	ToolListEnvironments = "list_environments"
	ToolShell            = "shell"
	ToolReadFile         = "read_file"
)

const (
	listEnvironmentsDescription = "List execution environments available to this run capability. Use the returned environment_id as the target of executor tools."
	shellDescription            = "Execute one deterministic argv vector in an environment. argv is never interpreted as a natural-language command or an implicit shell string."
	readFileDescription         = "Read one bounded block from a file relative to an environment root. Text is returned as UTF-8 when compact and otherwise as canonical base64."
)

// Tool is one frozen catalog entry. OutputSchema is a gateway/client contract;
// only Name, Description, and InputSchema are projected into Codex
// dynamicTools by harness-worker.
type Tool struct {
	Name          string
	Description   string
	InputSchema   json.RawMessage
	OutputSchema  json.RawMessage
	MapperVersion string
}

var tools = [...]Tool{
	{
		Name:          ToolListEnvironments,
		Description:   listEnvironmentsDescription,
		InputSchema:   json.RawMessage(listEnvironmentsInputSchema),
		OutputSchema:  json.RawMessage(listEnvironmentsOutputSchema),
		MapperVersion: "list-environments-v1",
	},
	{
		Name:          ToolShell,
		Description:   shellDescription,
		InputSchema:   json.RawMessage(shellInputSchema),
		OutputSchema:  json.RawMessage(shellOutputSchema),
		MapperVersion: "shell-v1",
	},
	{
		Name:          ToolReadFile,
		Description:   readFileDescription,
		InputSchema:   json.RawMessage(readFileInputSchema),
		OutputSchema:  json.RawMessage(readFileOutputSchema),
		MapperVersion: "read-file-v1",
	},
}

func Tools() []Tool {
	result := make([]Tool, len(tools))
	copy(result, tools[:])
	for index := range result {
		result[index].InputSchema = append(json.RawMessage(nil), result[index].InputSchema...)
		result[index].OutputSchema = append(json.RawMessage(nil), result[index].OutputSchema...)
	}
	return result
}

func Lookup(name string) (Tool, bool) {
	for _, tool := range tools {
		if tool.Name == name {
			copy := tool
			copy.InputSchema = append(json.RawMessage(nil), tool.InputSchema...)
			copy.OutputSchema = append(json.RawMessage(nil), tool.OutputSchema...)
			return copy, true
		}
	}
	return Tool{}, false
}

const listEnvironmentsInputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "executor_id": {
      "type": "string",
      "pattern": "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$",
      "description": "Optional canonical executor UUID filter."
    }
  }
}`

const listEnvironmentsOutputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["environments"],
  "properties": {
    "environments": {
      "type": "array",
      "maxItems": 256,
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["environment_id", "executor_id", "display_name", "platform", "default_cwd"],
        "properties": {
          "environment_id": {"type": "string", "pattern": "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"},
          "executor_id": {"type": "string", "pattern": "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"},
          "display_name": {"type": "string", "minLength": 1, "maxLength": 256},
          "description": {"type": "string", "maxLength": 2048},
          "platform": {"enum": ["linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64", "windows-amd64", "windows-arm64"]},
          "default_cwd": {"type": "string", "minLength": 1, "maxLength": 4096}
        }
      }
    }
  }
}`

const shellInputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["environment_id", "argv"],
  "properties": {
    "environment_id": {
      "type": "string",
      "pattern": "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$",
      "description": "Canonical environment UUID returned by list_environments."
    },
    "argv": {
      "type": "array",
      "minItems": 1,
      "maxItems": 256,
      "items": {"type": "string", "maxLength": 16384},
      "description": "Exact argv vector. Shell syntax requires an explicit shell executable in argv."
    },
    "cwd": {
      "type": "string",
      "minLength": 1,
      "maxLength": 4096,
      "description": "Environment-relative working directory. The versioned mapper uses the environment default when omitted."
    },
    "env": {
      "type": "object",
      "maxProperties": 256,
      "propertyNames": {"pattern": "^[A-Za-z_][A-Za-z0-9_]*$"},
      "additionalProperties": {"type": "string", "maxLength": 16384},
      "description": "Explicit environment entries. The shell-v1 mapper never inherits gateway or agentx ambient variables."
    },
    "timeout_ms": {
      "type": "integer",
      "minimum": 1,
      "maximum": 3600000,
      "description": "Hard execution deadline. The shell-v1 mapper uses 60000 when omitted."
    },
    "tty": {
      "type": "boolean",
      "description": "Allocate a PTY. The shell-v1 mapper uses false when omitted."
    }
  }
}`

const shellOutputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["process_id", "status", "chunks", "next_sequence", "sandbox_denied", "timed_out", "output_complete"],
  "properties": {
    "process_id": {"type": "string", "pattern": "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"},
    "status": {"enum": ["succeeded", "failed", "unknown"]},
    "reason_code": {"type": "string", "pattern": "^[a-z][a-z0-9_]{0,127}$"},
    "chunks": {
      "type": "array",
      "maxItems": 50000,
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["sequence", "stream", "chunk_base64"],
        "properties": {
          "sequence": {"type": "integer", "minimum": 1},
          "stream": {"enum": ["stdout", "stderr", "pty"]},
          "chunk_base64": {"type": "string", "contentEncoding": "base64"}
        }
      }
    },
    "next_sequence": {"type": "integer", "minimum": 1},
    "exit_code": {"type": ["integer", "null"]},
    "sandbox_denied": {"type": "boolean"},
    "timed_out": {"type": "boolean"},
    "output_complete": {"type": "boolean"}
  }
}`

const readFileInputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["environment_id", "path"],
  "properties": {
    "environment_id": {
      "type": "string",
      "pattern": "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$",
      "description": "Canonical environment UUID returned by list_environments."
    },
    "path": {
      "type": "string",
      "minLength": 1,
      "maxLength": 4096,
      "description": "Clean slash-separated path relative to the registered environment root. Absolute paths and parent traversal are rejected."
    },
    "offset": {
      "type": "integer",
      "minimum": 0,
      "maximum": 9007199254740991,
      "description": "Zero-based byte offset. The read-file-v1 mapper uses 0 when omitted."
    },
    "limit": {
      "type": "integer",
      "minimum": 1,
      "maximum": 1048576,
      "description": "Maximum bytes in this block. The read-file-v1 mapper uses 1048576 when omitted."
    }
  }
}`

const readFileOutputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["status", "path", "offset", "requested_bytes", "bytes_read", "eof", "encoding", "content"],
  "properties": {
    "status": {"enum": ["succeeded", "failed", "unknown"]},
    "path": {"type": "string", "minLength": 1, "maxLength": 4096},
    "offset": {"type": "integer", "minimum": 0, "maximum": 9007199254740991},
    "requested_bytes": {"type": "integer", "minimum": 1, "maximum": 1048576},
    "bytes_read": {"type": "integer", "minimum": 0, "maximum": 1048576},
    "eof": {"type": "boolean"},
    "encoding": {"enum": ["utf-8", "base64"]},
    "content": {"type": "string", "maxLength": 1398104}
  }
}`
