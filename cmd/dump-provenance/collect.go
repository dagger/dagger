package main

import (
	"context"
	"fmt"

	dagger "dagger.io/dagger"
)

type publicSelection struct {
	Items     []artifact       `json:"items"`
	Selection *publicSelection `json:"selection"`
}

func collect(ctx context.Context, client *dagger.Client, opts options) (*report, error) {
	fields := "items { path uri provenance { issues selectors { path include exclude conservative kind origin } } }"
	variables := map[string]any{}
	declarations := ""
	if opts.uri != "" {
		fields = "selection: filterUri(uri: $uri) { " + fields + " }"
		declarations = "$uri: String!"
		variables["uri"] = opts.uri
	}
	if opts.artifactType != "" {
		fields = "selection: filterTypes(types: $types) { " + fields + " }"
		if declarations != "" {
			declarations += ", "
		}
		declarations += "$types: [String!]!"
		variables["types"] = []string{opts.artifactType}
	}
	if declarations != "" {
		declarations = "(" + declarations + ")"
	}
	query := "query DumpProvenance" + declarations + " { currentWorkspace { snapshot { address cwd artifacts { " + fields + " } } } }"
	var data struct {
		CurrentWorkspace *struct {
			Snapshot *struct {
				Address   *string          `json:"address"`
				Cwd       *string          `json:"cwd"`
				Artifacts *publicSelection `json:"artifacts"`
			} `json:"snapshot"`
		} `json:"currentWorkspace"`
	}
	if err := client.Do(ctx, &dagger.Request{Query: query, Variables: variables, OpName: "DumpProvenance"}, &dagger.Response{Data: &data}); err != nil {
		return nil, fmt.Errorf("collect engine-backed provenance: %w", err)
	}
	if data.CurrentWorkspace == nil || data.CurrentWorkspace.Snapshot == nil {
		return nil, fmt.Errorf("public API response has no currentWorkspace.snapshot")
	}
	ws := data.CurrentWorkspace.Snapshot
	if ws.Address == nil || ws.Cwd == nil || ws.Artifacts == nil {
		return nil, fmt.Errorf("public API response has incomplete Workspace metadata or artifacts")
	}
	selection := ws.Artifacts
	for filters := 0; filters < len(variables); filters++ {
		if selection.Selection == nil {
			return nil, fmt.Errorf("public API response has an incomplete artifact filter")
		}
		selection = selection.Selection
	}
	result := &report{Collection: collection{Mode: "engine-backed", Snapshot: true, Workspace: *ws.Address, Cwd: *ws.Cwd, ArtifactType: opts.artifactType, URIFilter: opts.uri}, Artifacts: selection.Items}
	if err := result.validate(); err != nil {
		return nil, fmt.Errorf("public API response: %w", err)
	}
	return result, nil
}
