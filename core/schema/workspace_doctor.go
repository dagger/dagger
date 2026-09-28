package schema

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

// workspaceDoctor returns diagnostics instead of failing the query, so broken
// modules do not hide diagnostics for the remaining workspace modules.
func (s *workspaceSchema) workspaceDoctor(ctx context.Context, parent dagql.ObjectResult[*core.Workspace], _ struct{}) (core.JSON, error) {
	mods, failures, err := s.workspacePrimaryModules(ctx, parent, nil, core.ModuleLoadBestEffort)
	if err != nil {
		return nil, err
	}
	type diagnostic struct {
		Name  string `json:"name"`
		Check string `json:"check"`
		Error string `json:"error,omitempty"`
	}
	results := []diagnostic{}
	for _, failure := range failures {
		results = append(results, diagnostic{Name: failure.Name, Check: "Module loading", Error: failure.Message})
	}
	for _, mod := range mods {
		name := mod.Self().Name()
		results = append(results, diagnostic{Name: name, Check: "Module loading"})
		settings := diagnostic{Name: name, Check: "Module settings"}
		if err := errors.Join(core.ValidateWorkspaceModuleSettings(ctx, mod)...); err != nil {
			settings.Error = err.Error()
		}
		results = append(results, settings)
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].Name < results[j].Name })
	return json.Marshal(results)
}
