package a2ui

import "fmt"

// CommandCard builds an A2UI card showing a shell command, its output, and a
// status line, bound to a per-item surface. id is the codex item id.
func CommandCard(id, command, output, statusLine string) []Message {
	surface := "cmd-" + id
	return []Message{
		{Version: Version, CreateSurface: &CreateSurface{SurfaceID: surface, CatalogID: CatalogID, SendDataModel: true}},
		{Version: Version, UpdateComponents: &UpdateComponents{SurfaceID: surface, Components: []Component{
			{ID: "root", Component: "Card", Child: "col"},
			{ID: "col", Component: "Column", Children: []string{"cmd", "out", "status"}},
			{ID: "cmd", Component: "Text", Text: bind("/command")},
			{ID: "out", Component: "Text", Text: bind("/output")},
			{ID: "status", Component: "Text", Text: bind("/status")},
		}}},
		{Version: Version, UpdateDataModel: &UpdateDataModel{SurfaceID: surface, Value: map[string]string{
			"command": command,
			"output":  output,
			"status":  statusLine,
		}}},
	}
}

// FileChange is one changed file for FileDiffCard.
type FileChange struct {
	Path string
	Kind string // "add" | "delete" | "update"
	Diff string
}

// FileDiffCard builds an A2UI card summarizing file changes: a header plus one
// Text node per file carrying "<kind> <path>" and the diff. id is the item id.
func FileDiffCard(id string, files []FileChange) []Message {
	surface := "file-" + id
	comps := []Component{
		{ID: "root", Component: "Card", Child: "col"},
	}
	children := []string{"header"}
	comps = append(comps, Component{ID: "header", Component: "Text", Text: bind("/header")})
	data := map[string]string{"header": fmt.Sprintf("%d file(s) changed", len(files))}
	for i, f := range files {
		pathID := fmt.Sprintf("path%d", i)
		diffID := fmt.Sprintf("diff%d", i)
		children = append(children, pathID, diffID)
		comps = append(comps,
			Component{ID: pathID, Component: "Text", Text: bind("/" + pathID)},
			Component{ID: diffID, Component: "Text", Text: bind("/" + diffID)},
		)
		data[pathID] = fmt.Sprintf("%s %s", f.Kind, f.Path)
		data[diffID] = f.Diff
	}
	comps = append([]Component{comps[0]}, append([]Component{{ID: "col", Component: "Column", Children: children}}, comps[1:]...)...)
	return []Message{
		{Version: Version, CreateSurface: &CreateSurface{SurfaceID: surface, CatalogID: CatalogID, SendDataModel: true}},
		{Version: Version, UpdateComponents: &UpdateComponents{SurfaceID: surface, Components: comps}},
		{Version: Version, UpdateDataModel: &UpdateDataModel{SurfaceID: surface, Value: data}},
	}
}
