package schema

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

// sessionModuleMatches reports whether a module that is loaded from a
// workspace value or from a staged edit is the module that the session loaded
// from the host workspace under the same name: the same settings, the same
// .env fallback, and the same source. The source comparison covers the
// module's files, its .env defaults and its dependencies.
//
// Object settings resolve with the resources of the session's main client
// (see core.Module.UntrustedSettings). A match gives the module only what the
// host configuration already gives the session's own module, so it keeps its
// settings. A failure to find or compare the session module is not a match.
func sessionModuleMatches(
	ctx context.Context,
	name string,
	settings map[string]any,
	defaultsFromDotEnv bool,
	src dagql.ObjectResult[*core.ModuleSource],
) bool {
	query, err := core.CurrentQuery(ctx)
	if err != nil {
		return false
	}
	mainClient, err := query.NonModuleParentClientMetadata(ctx)
	if err != nil {
		return false
	}
	mainCtx := engine.ContextWithClientMetadata(ctx, mainClient)

	hostMod, ok, err := query.Server.SessionWorkspaceModule(mainCtx, name)
	if err != nil || !ok {
		return false
	}
	host := hostMod.Self()
	if host.UntrustedSettings ||
		host.DefaultsFromDotEnv != (defaultsFromDotEnv && len(settings) > 0) ||
		!host.Source.Valid {
		return false
	}
	same, err := sameModuleSettings(host.WorkspaceConfig, settings)
	if err != nil || !same {
		return false
	}
	return sameModuleSource(mainCtx, host.Source.Value, src)
}

// sameModuleSource reports whether two module sources hold the same module:
// the same implementation inputs (see ModuleSource.SourceImplementationDigest),
// the same files, and dependencies that are the same by this rule.
//
// Directory digests also cover owners and permissions, which differ between a
// host checkout and a copy of it, so files are compared as a changeset compares
// them: paths, contents and Git file modes.
func sameModuleSource(ctx context.Context, host, other dagql.ObjectResult[*core.ModuleSource]) bool {
	a, b := host.Self(), other.Self()
	if a == nil || b == nil {
		return a == b
	}
	if !sameModuleSources(ctx, a.Dependencies, b.Dependencies) ||
		!sameModuleSources(ctx, a.Toolchains, b.Toolchains) ||
		(a.Blueprint.Self() == nil) != (b.Blueprint.Self() == nil) ||
		(a.Blueprint.Self() != nil && !sameModuleSource(ctx, a.Blueprint, b.Blueprint)) {
		return false
	}

	// With the parts compared above taken from host, every other input of the
	// implementation digest must be equal.
	swapped := b.Clone()
	swapped.ContextDirectory = a.ContextDirectory
	swapped.Dependencies = a.Dependencies
	swapped.Toolchains = a.Toolchains
	swapped.Blueprint = a.Blueprint
	hostDigest, err := a.SourceImplementationDigest(ctx)
	if err != nil {
		return false
	}
	digest, err := swapped.SourceImplementationDigest(ctx)
	if err != nil || digest != hostDigest {
		return false
	}

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return false
	}
	hostContextID, err := a.ContextDirectory.ID()
	if err != nil {
		return false
	}
	var sameFiles bool
	if err := dag.Select(ctx, b.ContextDirectory, &sameFiles,
		dagql.Selector{Field: "changes", Args: []dagql.NamedInput{
			{Name: "from", Value: dagql.NewID[*core.Directory](hostContextID)},
		}},
		dagql.Selector{Field: "isEmpty"},
	); err != nil {
		return false
	}
	return sameFiles
}

func sameModuleSources(ctx context.Context, host, other dagql.ObjectResultArray[*core.ModuleSource]) bool {
	if len(host) != len(other) {
		return false
	}
	for i := range host {
		if !sameModuleSource(ctx, host[i], other[i]) {
			return false
		}
	}
	return true
}

// sameModuleSettings compares settings in the JSON form that asModule receives
// them in (see BuildLegacyAsModuleArgs).
func sameModuleSettings(a, b map[string]any) (bool, error) {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b), nil
	}
	ja, err := canonicalSettingsJSON(a)
	if err != nil {
		return false, err
	}
	jb, err := canonicalSettingsJSON(b)
	if err != nil {
		return false, err
	}
	return ja == jb, nil
}

func canonicalSettingsJSON(settings map[string]any) (string, error) {
	raw, err := json.Marshal(settings)
	if err != nil {
		return "", fmt.Errorf("encode settings: %w", err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", fmt.Errorf("decode settings: %w", err)
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return "", fmt.Errorf("encode settings: %w", err)
	}
	return string(canonical), nil
}
