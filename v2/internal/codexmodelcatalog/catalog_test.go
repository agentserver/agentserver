package codexmodelcatalog

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestDirectCatalogPreservesEveryOtherStockField(t *testing.T) {
	raw, err := Direct()
	if err != nil {
		t.Fatal(err)
	}
	var original, direct map[string]any
	if err := json.Unmarshal(upstream, &original); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &direct); err != nil {
		t.Fatal(err)
	}
	foundProductionModel := false
	for _, value := range original["models"].([]any) {
		model := value.(map[string]any)
		if model["slug"] == "gpt-5.6-sol" {
			foundProductionModel = true
			if model["tool_mode"] != "code_mode_only" {
				t.Fatal("upstream production-model regression fixture changed")
			}
		}
		model["tool_mode"] = "direct"
		model["use_responses_lite"] = false
	}
	if !foundProductionModel || !reflect.DeepEqual(original, direct) {
		t.Fatal("direct routing changed other model metadata")
	}
}
