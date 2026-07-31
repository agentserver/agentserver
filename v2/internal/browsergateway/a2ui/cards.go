package a2ui

import "fmt"

type CommandView struct {
	Command string
	Output  string
	Status  string
}

type FileChange struct {
	Path string
	Kind string
	Diff string
}

// CommandCard returns createSurface, updateComponents, and updateDataModel for
// one deterministic command-result card.
func CommandCard(id string, command CommandView) []Message {
	surfaceID := "command-" + id
	return []Message{
		{
			Version: Version,
			CreateSurface: &CreateSurface{
				SurfaceID: surfaceID,
				CatalogID: CatalogID,
			},
		},
		{
			Version: Version,
			UpdateComponents: &UpdateComponents{SurfaceID: surfaceID, Components: []Component{
				{ID: "root", Component: "Card", Child: "content"},
				{ID: "content", Component: "Column", Children: []string{"title", "command", "output", "status"}},
				{ID: "title", Component: "Text", Text: "Command"},
				{ID: "command", Component: "Text", Text: bind("/command")},
				{ID: "output", Component: "Text", Text: bind("/output")},
				{ID: "status", Component: "Text", Text: bind("/status")},
			}},
		},
		{
			Version: Version,
			UpdateDataModel: &UpdateDataModel{SurfaceID: surfaceID, Value: map[string]string{
				"command": command.Command,
				"output":  command.Output,
				"status":  command.Status,
			}},
		},
	}
}

// FileChangeCard returns a display-only card with a deterministic pair of path
// and diff nodes for each changed file.
func FileChangeCard(id string, files []FileChange) []Message {
	surfaceID := "file-change-" + id
	components := []Component{
		{ID: "root", Component: "Card", Child: "content"},
	}
	children := []string{"title"}
	components = append(components, Component{ID: "title", Component: "Text", Text: bind("/title")})
	data := map[string]string{"title": fmt.Sprintf("%d file(s) changed", len(files))}
	for index, file := range files {
		pathID := fmt.Sprintf("file-%03d-path", index)
		diffID := fmt.Sprintf("file-%03d-diff", index)
		children = append(children, pathID, diffID)
		components = append(components,
			Component{ID: pathID, Component: "Text", Text: bind("/" + pathID)},
			Component{ID: diffID, Component: "Text", Text: bind("/" + diffID)},
		)
		data[pathID] = fmt.Sprintf("%s %s", file.Kind, file.Path)
		data[diffID] = file.Diff
	}
	components = append([]Component{components[0], {ID: "content", Component: "Column", Children: children}}, components[1:]...)
	return []Message{
		{Version: Version, CreateSurface: &CreateSurface{SurfaceID: surfaceID, CatalogID: CatalogID}},
		{Version: Version, UpdateComponents: &UpdateComponents{SurfaceID: surfaceID, Components: components}},
		{Version: Version, UpdateDataModel: &UpdateDataModel{SurfaceID: surfaceID, Value: data}},
	}
}
