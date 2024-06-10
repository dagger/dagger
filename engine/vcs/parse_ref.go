package vcs

import (
	"context"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/tonistiigi/fsutil/types"
)

type parsedRefString struct {
	ModPath        string
	ModVersion     string
	HasVersion     bool
	Kind           core.ModuleSourceKind
	RepoRoot       *RepoRoot
	RepoRootSubdir string
}

// Defines a function type that matches the signature of ParseRefString
// It is used to be able to create generic functions parsing two types of refs:
// - one stating a dir (ParseRefStringDir)
// - one stating a file (ParseRefStringFile)
type ParseRefFunc func(ctx context.Context, bk buildkitClient, refString string) parsedRefString

// Interface used across several context:
// - Engine context (e.g. core/schema) => query.Buildkit
// - CLI: (e.g. cmd/flags.go) => osBuildkitClient
// - host / engine interaction mocking => MockBuildkitClient
type buildkitClient interface {
	StatCallerHostPath(ctx context.Context, path string, followLinks bool) (*types.Stat, error)
}

// parseRefStringDir parses a ref string into its components
// New heuristic:
// - stat folder to see if dir is present
// - if not, try to isolate root of git repo from the ref
// - if nothing worked, fallback as local ref, as before
func ParseRefStringDir(ctx context.Context, bk buildkitClient, refString string) parsedRefString {
	var parsed parsedRefString
	parsed.ModPath, parsed.ModVersion, parsed.HasVersion = strings.Cut(refString, "@")

	// We do a stat in case the mod path github.com/username is a local directory
	stat, err := bk.StatCallerHostPath(ctx, parsed.ModPath, false)
	if err == nil {
		if !parsed.HasVersion && stat.IsDir() {
			parsed.Kind = core.ModuleSourceKindLocal
			return parsed
		}
	}

	// we try to isolate the root of the git repo
	repoRoot, err := RepoRootForImportPath(parsed.ModPath, false)
	if err == nil && repoRoot != nil && repoRoot.VCS != nil && repoRoot.VCS.Name == "Git" {
		parsed.Kind = core.ModuleSourceKindGit
		parsed.RepoRoot = repoRoot
		parsed.RepoRootSubdir = strings.TrimPrefix(parsed.ModPath, repoRoot.Root)
		// the extra "/" is important as subpath traversal such as /../ are being cleaned by filePath.Clean
		parsed.RepoRootSubdir = strings.TrimPrefix(parsed.RepoRootSubdir, "/")
		return parsed
	}

	parsed.Kind = core.ModuleSourceKindLocal
	return parsed
}

// parseRefStringFile parses a ref string into its components
// New heuristic:
// - stat file to see if it is present
// - if not, try to isolate root of git repo from the ref
// - if nothing worked, fallback as local ref, as before
func ParseRefStringFile(ctx context.Context, bk buildkitClient, refString string) parsedRefString {
	var parsed parsedRefString
	parsed.ModPath, parsed.ModVersion, parsed.HasVersion = strings.Cut(refString, "@")

	// We do a stat in case the mod path github.com/username is a local directory
	stat, err := bk.StatCallerHostPath(ctx, parsed.ModPath, false)
	if err == nil {
		if !parsed.HasVersion && !stat.IsDir() {
			parsed.Kind = core.ModuleSourceKindLocal
			return parsed
		}
	}

	// we try to isolate the root of the git repo
	repoRoot, err := RepoRootForImportPath(parsed.ModPath, false)
	if err == nil && repoRoot != nil && repoRoot.VCS != nil && repoRoot.VCS.Name == "Git" {
		parsed.Kind = core.ModuleSourceKindGit
		parsed.RepoRoot = repoRoot
		parsed.RepoRootSubdir = strings.TrimPrefix(parsed.ModPath, repoRoot.Root)
		// the extra "/" is important as subpath traversal such as /../ are being cleaned by filePath.Clean
		parsed.RepoRootSubdir = strings.TrimPrefix(parsed.RepoRootSubdir, "/")
		return parsed
	}

	parsed.Kind = core.ModuleSourceKindLocal
	return parsed
}
