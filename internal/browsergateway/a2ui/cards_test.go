package a2ui

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCommandCard_Shape(t *testing.T) {
	msgs := CommandCard("cmd-1", "ls -la", "total 0", "exit 0")
	if len(msgs) != 3 {
		t.Fatalf("want 3 messages (createSurface, updateComponents, updateDataModel), got %d", len(msgs))
	}
	if msgs[0].CreateSurface == nil || msgs[0].CreateSurface.SurfaceID != "cmd-cmd-1" {
		t.Fatalf("msg[0] not a createSurface for surface cmd-cmd-1: %+v", msgs[0])
	}
	if msgs[0].CreateSurface.CatalogID != CatalogID {
		t.Errorf("catalogId = %q", msgs[0].CreateSurface.CatalogID)
	}
	if msgs[1].UpdateComponents == nil {
		t.Fatal("msg[1] not updateComponents")
	}
	// exactly one root component
	roots := 0
	for _, c := range msgs[1].UpdateComponents.Components {
		if c.ID == "root" {
			roots++
			if c.Component != "Card" || c.Child == "" {
				t.Errorf("root should be a Card with a child, got %+v", c)
			}
		}
	}
	if roots != 1 {
		t.Fatalf("want exactly 1 root component, got %d", roots)
	}
	if msgs[2].UpdateDataModel == nil {
		t.Fatal("msg[2] not updateDataModel")
	}
	// every message serializes with version v0.9 and exactly one payload key
	for i, m := range msgs {
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("msg[%d] marshal: %v", i, err)
		}
		s := string(b)
		if !strings.Contains(s, `"version":"v0.9"`) {
			t.Errorf("msg[%d] missing version v0.9: %s", i, s)
		}
	}
}

func TestFileDiffCard_Shape(t *testing.T) {
	msgs := FileDiffCard("fc-1", []FileChange{{Path: "a.go", Kind: "update", Diff: "@@ -1 +1 @@"}})
	if len(msgs) != 3 || msgs[0].CreateSurface == nil || msgs[0].CreateSurface.SurfaceID != "file-fc-1" {
		t.Fatalf("file diff card surface wrong: %+v", msgs[0])
	}
	if msgs[1].UpdateComponents == nil {
		t.Fatal("msg[1] not updateComponents")
	}
}
