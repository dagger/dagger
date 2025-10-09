package schema

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type checksSchema struct{}

var _ SchemaResolvers = &checksSchema{}

func (s checksSchema) Install(srv *dagql.Server) {
	// Top-level constructor: checks (returns array of Check)
	dagql.Fields[*core.Query]{
		dagql.Func("checks", s.checks).
			Doc("Get array of checks"),
	}.Install(srv)

	// Check methods
	dagql.Fields[*core.Check]{
		dagql.Func("success", s.success).Doc("Return whether the check succeeded"),
		dagql.Func("message", s.message).Doc("Return the check message"),
		dagql.Func("summary", s.summary).Doc("Return a summary of the check"),
	}.Install(srv)
}

func (s checksSchema) checks(ctx context.Context, q *core.Query, args struct{}) ([]*core.Check, error) {
	// Get the modules being served to the current client
	deps, err := q.CurrentServedDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get served dependencies: %w", err)
	}

	// Collect all check functions from all modules
	var checks []*core.Check

	// Iterate through all modules
	for _, mod := range deps.Mods {
		// Type assert to *Module to access ObjectDefs
		module, ok := mod.(*core.Module)
		if !ok {
			// Skip non-user modules (e.g., core modules)
			continue
		}
		_, modSpan := core.Tracer(ctx).Start(ctx, fmt.Sprintf("[checks] mod=%q", module.OriginalName))
		modSpan.End()
		// Find the main object for this module
		// The main object is the one whose OriginalName matches the module's OriginalName
		var mainObject *core.ObjectTypeDef
		for _, objDef := range module.ObjectDefs {
			if objDef.AsObject.Valid {
				obj := objDef.AsObject.Value

				// Check if this is the main object by comparing normalized names
				if strings.EqualFold(obj.OriginalName, strings.ReplaceAll(module.OriginalName, "-", "")) {
					_, objSpan := core.Tracer(ctx).Start(ctx, fmt.Sprintf("[checks] mod=%q obj=%q", module.OriginalName, obj.OriginalName))
					objSpan.End()
					mainObject = obj
					break
				}
			}
		}
		// If no main object found, skip this module
		if mainObject == nil {
			continue
		}
		// Search for functions starting with "Check" in the main object
		for _, fn := range mainObject.Functions {
			_, fnSpan := core.Tracer(ctx).Start(ctx, fmt.Sprintf("[checks] mod=%q obj=%q fn=%q", module.OriginalName, mainObject.OriginalName, fn.OriginalName))
			fnSpan.End()

			// Check if the function name starts with "Check"
			if name := strings.ToLower(fn.OriginalName); strings.HasPrefix(name, "check") {
				check := &core.Check{
					Name:         strings.TrimPrefix(name, "check"),
					Description:  fn.Description,
					ModuleName:   module.Name(),
					FunctionName: fn.Name,
				}
				if module.Source.Valid {
					src := module.Source.Value.Self()
					switch src.Kind {
					case core.ModuleSourceKindLocal:
						check.Context = path.Clean(path.Join(src.Local.ContextDirectoryPath, src.SourceRootSubpath))
					case core.ModuleSourceKindGit:
						check.Context = src.Git.RepoRootPath + ":" + src.SourceRootSubpath
					}
				}
				checks = append(checks, check)
			}
		}
	}
	return checks, nil
}

func (s checksSchema) success(ctx context.Context, parent *core.Check, args struct{}) (bool, error) {
	success, _, err := parent.Run(ctx)
	return success, err
}

func (s checksSchema) message(ctx context.Context, parent *core.Check, args struct{}) (string, error) {
	_, message, err := parent.Run(ctx)
	return message, err
}

func (s checksSchema) summary(ctx context.Context, parent *core.Check, args struct{}) (string, error) {
	success, message, err := parent.Run(ctx)
	if err != nil {
		return "", err
	}
	name := parent.Description
	if name == "" {
		name = parent.Name
	}
	if success {
		return fmt.Sprintf("%s: ✅ %s", parent.Context, name), nil
	}
	return fmt.Sprintf("%s: ⛔️ %s: %s", parent.Context, name, message), nil
}
