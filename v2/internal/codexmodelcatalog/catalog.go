// Package codexmodelcatalog preserves stock model metadata while pinning the
// AgentServer dynamic-executor integration to direct tools. Model-level
// tool_mode takes precedence over Codex's code_mode feature flags.
package codexmodelcatalog

import (
	_ "embed"
	"encoding/json"
	"errors"
)

// upstream is the unmodified models-manager catalog from openai/codex tag
// rust-v0.160.1 (Apache-2.0). Keep model names, context limits, reasoning and
// prompt metadata intact; only the application-owned tool routing changes.
//
//go:embed upstream-0.160.1.json
var upstream []byte

const FileName = "model-catalog.json"

func Direct() ([]byte, error) {
	var catalog map[string]json.RawMessage
	if err := json.Unmarshal(upstream, &catalog); err != nil {
		return nil, err
	}
	var models []map[string]json.RawMessage
	if err := json.Unmarshal(catalog["models"], &models); err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return nil, errors.New("stock model catalog is empty")
	}
	for _, model := range models {
		model["tool_mode"] = json.RawMessage(`"direct"`)
		// llmproxy uses standard Responses with structured tool definitions,
		// not model-specific instructions-only Responses Lite.
		model["use_responses_lite"] = json.RawMessage(`false`)
	}
	raw, err := json.Marshal(models)
	if err != nil {
		return nil, err
	}
	catalog["models"] = raw
	return json.Marshal(catalog)
}
