package sdk

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

const gitCredentialsArgName = "gitCredentials"

// files scanned for git dependency URLs; the referenced hosts become the
// git-credential socket's exact-host allowlist. Lockfiles are included so
// transitive git dependencies resolve too.
var gitCredentialManifestFilesBySDK = map[string][]string{
	"python-sdk":     {"pyproject.toml", "uv.lock", "requirements.lock"},
	"typescript-sdk": {"package.json", "package-lock.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock"},
}

var gitCredentialManifestURLPatterns = []*regexp.Regexp{
	// explicit git+http(s) URLs (PEP 508, package.json git deps, lockfiles)
	regexp.MustCompile(`git\+https?://(?:[^/@\s"']*@)?([^/\s"'#]+)`),
	// uv's [tool.uv.sources] / uv.lock form: pkg = { git = "https://host/..." }
	regexp.MustCompile(`git\s*=\s*"https?://(?:[^/@\s"']*@)?([^/\s"'#]+)`),
}

func gitCredentialHostsFromManifests(manifests ...string) []string {
	var hosts []string
	for _, manifest := range manifests {
		for _, pattern := range gitCredentialManifestURLPatterns {
			for _, match := range pattern.FindAllStringSubmatch(manifest, -1) {
				hosts = append(hosts, match[1])
			}
		}
	}
	return core.NormalizeGitCredentialHosts(hosts)
}

// mintGitCredentialSocket mints a git-credential socket for the given hosts
// under trusted dependency resolution, so it may serve the non-module parent
// client's credentials (session's originating client as fallback).
func mintGitCredentialSocket(ctx context.Context, root *core.Query, hosts []string) (dagql.ID[*core.Socket], error) {
	var zero dagql.ID[*core.Socket]

	dag, err := root.Server.Server(ctx)
	if err != nil {
		return zero, fmt.Errorf("failed to get dag for git credential socket: %w", err)
	}

	ctx = core.WithModuleDependencyResolution(ctx)

	hostsInput := make(dagql.ArrayInput[dagql.String], len(hosts))
	for i, host := range hosts {
		hostsInput[i] = dagql.NewString(host)
	}
	var sock dagql.Result[*core.Socket]
	if err := dag.Select(ctx, dag.Root(), &sock,
		dagql.Selector{Field: "host"},
		dagql.Selector{Field: "_gitCredential", Args: []dagql.NamedInput{
			{Name: "hosts", Value: hostsInput},
		}},
	); err != nil {
		return zero, fmt.Errorf("failed to select git credential socket: %w", err)
	}
	sockID, err := sock.ID()
	if err != nil {
		return zero, fmt.Errorf("failed to get git credential socket ID: %w", err)
	}
	return dagql.NewID[*core.Socket](sockID), nil
}

// gitCredentialsInput returns the optional gitCredentials argument for the
// named SDK function, or nil when it doesn't apply: the SDK must be builtin
// (git-loaded and external SDKs fail closed), the function must declare the
// argument, and the module's manifests must reference git+http(s)
// dependencies.
func (sdk *module) gitCredentialsInput(ctx context.Context, dag *dagql.Server, funcName string, source dagql.ObjectResult[*core.ModuleSource]) (*dagql.NamedInput, error) {
	if !sdk.builtin {
		return nil, nil
	}
	fn, ok := sdk.funcs[funcName]
	if !ok {
		return nil, nil
	}
	spec, err := fn.FieldSpec(ctx, core.NewUserMod(sdk.mod))
	if err != nil {
		return nil, fmt.Errorf("failed to get %s field spec: %w", funcName, err)
	}
	declared := false
	for _, input := range spec.Args.Inputs(dag.View) {
		if input.Name == gitCredentialsArgName {
			declared = true
			break
		}
	}
	if !declared {
		return nil, nil
	}

	hosts, err := sdk.manifestGitCredentialHosts(ctx, source)
	if err != nil || len(hosts) == 0 {
		return nil, err
	}
	sockID, err := mintGitCredentialSocket(ctx, sdk.root, hosts)
	if err != nil {
		return nil, err
	}
	return &dagql.NamedInput{Name: gitCredentialsArgName, Value: dagql.Opt(sockID)}, nil
}

func (sdk *module) manifestGitCredentialHosts(ctx context.Context, source dagql.ObjectResult[*core.ModuleSource]) ([]string, error) {
	if source.Self() == nil || source.Self().ContextDirectory.Self() == nil {
		return nil, nil
	}
	manifestFiles := gitCredentialManifestFilesBySDK[sdk.mod.Self().Name()]
	if len(manifestFiles) == 0 {
		return nil, nil
	}
	rootDag, err := sdk.root.Server.Server(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag for manifest scan: %w", err)
	}

	var manifests []string
	for _, name := range manifestFiles {
		var contents dagql.String
		if err := rootDag.Select(ctx, source.Self().ContextDirectory, &contents,
			dagql.Selector{Field: "file", Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(filepath.Join(source.Self().SourceSubpath, name))},
			}},
			dagql.Selector{Field: "contents"},
		); err != nil {
			// treated as "manifest absent": a real read failure only means no
			// socket, and the install then fails with a visible auth error
			continue
		}
		manifests = append(manifests, contents.String())
	}
	return gitCredentialHostsFromManifests(manifests...), nil
}
