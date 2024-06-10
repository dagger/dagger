package vcs

import (
	"context"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/moby/buildkit/util/gitutil"
)

func ConvertToBuildKitRef(ctx context.Context, ref string, bk buildkitClient, parseRef ParseRefFunc) (string, core.ModuleSourceKind) {
	// explicit local ref
	if strings.HasPrefix(ref, "file://") {
		return ref, core.ModuleSourceKindLocal
	}

	// retro-compatibility with previous remote BuildKit ref
	if _, err := gitutil.ParseURL(ref); err == nil {
		return ref, core.ModuleSourceKindGit
	}

	// New ref parsing
	parsed := parseRef(ctx, bk, ref)
	if parsed.Kind == core.ModuleSourceKindLocal {
		return parsed.ModPath, core.ModuleSourceKindLocal
	}

	var sb strings.Builder
	sb.Write([]byte(parsed.RepoRoot.Repo))
	if parsed.HasVersion {
		sb.Write([]byte("#" + parsed.ModVersion))
	}
	if parsed.RepoRootSubdir != "" {
		sb.Write([]byte(":" + parsed.RepoRootSubdir))
	}

	return sb.String(), core.ModuleSourceKindGit
}
