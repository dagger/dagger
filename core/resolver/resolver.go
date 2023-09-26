package resolver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"dagger.io/dagger"
	"github.com/dagger/dagger/core/moduleconfig"
)

// ModuleRef contains all of the information we're able to learn about a provided
// module ref.
type ModuleRef struct {
	Path    string // Path is the provided path for the module.
	Version string // Version is the provided version for the module, if any.

	Local bool    // Local indicates that the module's Path is just a local path.
	Git   *GitRef // Git is the resolved Git information.

	SubPath string // Subdir is the subdirectory within the fetched source.
}

type GitRef struct {
	HTMLURL  string // HTMLURL is the URL a user can use to browse the repo.
	CloneURL string // CloneURL is the URL to clone.
	Commit   string // Commit is the commit to check out.
}

func (ref *ModuleRef) String() string {
	if ref.Local {
		// TODO(vito): This may be worth a rethink, but the idea is for local
		// modules to be represented as a 'subpath' of their outer module, that way
		// they can do things like refer to sibling modules at ../foo. But this
		// hasn't been proved out. Anyway, at this layer we need to preserve the
		// subpath because this gets printed to `dagger.json`, and without this the
		// module will depend on itself, leading to an infinite loop.
		return path.Join(ref.Path, ref.SubPath)
	}
	if ref.Version == "" {
		return ref.Path
	}
	return fmt.Sprintf("%s@%s", ref.Path, ref.Version)
}

func (ref *ModuleRef) Config(ctx context.Context, c *dagger.Client) (*moduleconfig.Config, error) {
	switch {
	case ref.Local:
		configBytes, err := os.ReadFile(path.Join(ref.Path, moduleconfig.Filename))
		if err != nil {
			return nil, fmt.Errorf("failed to read local config file: %w", err)
		}
		var cfg moduleconfig.Config
		if err := json.Unmarshal(configBytes, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse local config file: %w", err)
		}
		return &cfg, nil

	case ref.Git != nil:
		if c == nil {
			return nil, fmt.Errorf("cannot load git module config with nil dagger client")
		}
		repoDir := c.Git(ref.Git.CloneURL).Commit(ref.Version).Tree()
		var configPath string
		if ref.SubPath != "" {
			configPath = path.Join(ref.SubPath, moduleconfig.Filename)
		} else {
			configPath = moduleconfig.Filename
		}
		configStr, err := repoDir.File(configPath).Contents(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to read git config file: %w", err)
		}
		var cfg moduleconfig.Config
		if err := json.Unmarshal([]byte(configStr), &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse git config file: %w", err)
		}
		return &cfg, nil

	default:
		panic("invalid module ref")
	}
}

func (ref *ModuleRef) AsModule(ctx context.Context, c *dagger.Client) (*dagger.Module, error) {
	cfg, err := ref.Config(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to get module config: %w", err)
	}

	switch {
	case ref.Local:
		// TODO put this somewhere
		modSrcDir, err := filepath.Abs(ref.Path)
		if err != nil {
			return nil, fmt.Errorf("failed to get module root: %w", err)
		}
		modRootDir, err := filepath.Abs(filepath.Join(ref.Path, cfg.Root))
		if err != nil {
			return nil, fmt.Errorf("failed to get module root: %w", err)
		}
		subdirRelPath, err := filepath.Rel(modRootDir, modSrcDir)
		if err != nil {
			return nil, fmt.Errorf("failed to get subdir relative path: %w", err)
		}
		if strings.HasPrefix(subdirRelPath, "..") {
			return nil, fmt.Errorf("module config path %q is not under module root %q", ref.Path, modRootDir)
		}

		return c.Host().Directory(modRootDir, dagger.HostDirectoryOpts{
			Include: cfg.Include,
			Exclude: cfg.Exclude,
		}).AsModule(dagger.DirectoryAsModuleOpts{
			SourceSubpath: subdirRelPath,
		}), nil

	case ref.Git != nil:
		rootPath := path.Clean(path.Join(ref.SubPath, cfg.Root))
		if strings.HasPrefix(rootPath, "..") {
			return nil, fmt.Errorf("module config path %q is not under module root %q", ref.SubPath, rootPath)
		}

		return c.Git(ref.Git.CloneURL).Commit(ref.Version).Tree().
			Directory(rootPath).
			AsModule(), nil

	default:
		return nil, fmt.Errorf("invalid module (local=%t, git=%t)", ref.Local, ref.Git != nil)
	}
}

// TODO dedup with ResolveMovingRef
func ResolveStableRef(modQuery string) (*ModuleRef, error) {
	modPath, modVersion, hasVersion := strings.Cut(modQuery, "@")

	ref := &ModuleRef{
		Path: modPath,
	}

	// TODO: figure out how to support arbitrary repos in a predictable way.
	// Maybe piggyback on whatever Go supports? (the whole <meta go-import>
	// thing)
	isGitHub := strings.HasPrefix(modPath, "github.com/")

	if !hasVersion {
		if isGitHub {
			return nil, fmt.Errorf("no version provided for remote ref: %s", modQuery)
		}

		// assume local path
		//
		// NB(vito): HTTP URLs should be supported by taking a sha256 digest as the
		// version. so it should be safe to assume no version = local path. as a
		// rule, if it's local we don't need to version it; if it's remote, we do.
		ref.Local = true
		return ref, nil
	}

	ref.Git = &GitRef{} // assume git for now, HTTP can come later

	if !isGitHub {
		return nil, fmt.Errorf("for now, only github.com/ paths are supported: %s", modPath)
	}

	segments := strings.SplitN(modPath, "/", 4)
	if len(segments) < 3 {
		return nil, fmt.Errorf("invalid github.com path: %s", modPath)
	}

	ref.Git.CloneURL = "https://" + segments[0] + "/" + segments[1] + "/" + segments[2]

	if !hasVersion {
		return nil, fmt.Errorf("no version provided for %s", modPath)
	}

	ref.Version = modVersion    // assume commit
	ref.Git.Commit = modVersion // assume commit

	if len(segments) == 4 {
		ref.SubPath = segments[3]
		ref.Git.HTMLURL = "https://" + segments[0] + "/" + segments[1] + "/" + segments[2] + "/tree/" + ref.Version + "/" + ref.SubPath
	} else {
		ref.Git.HTMLURL = "https://" + segments[0] + "/" + segments[1] + "/" + segments[2]
	}

	return ref, nil
}

func ResolveMovingRef(ctx context.Context, dag *dagger.Client, modQuery string) (*ModuleRef, error) {
	modPath, modVersion, hasVersion := strings.Cut(modQuery, "@")

	ref := &ModuleRef{
		Path: modPath,
	}

	// TODO: figure out how to support arbitrary repos in a predictable way.
	// Maybe piggyback on whatever Go supports? (the whole <meta go-import>
	// thing)
	isGitHub := strings.HasPrefix(modPath, "github.com/")

	if !hasVersion && !isGitHub {
		// assume local path
		//
		// NB(vito): HTTP URLs should be supported by taking a sha256 digest as the
		// version. so it should be safe to assume no version = local path. as a
		// rule, if it's local we don't need to version it; if it's remote, we do.
		ref.Local = true
		return ref, nil
	}

	ref.Git = &GitRef{} // assume git for now, HTTP can come later

	if !isGitHub {
		return nil, fmt.Errorf("for now, only github.com/ paths are supported: %q", modQuery)
	}

	segments := strings.SplitN(modPath, "/", 4)
	if len(segments) < 3 {
		return nil, fmt.Errorf("invalid github.com path: %s", modPath)
	}

	ref.Git.CloneURL = "https://" + segments[0] + "/" + segments[1] + "/" + segments[2]

	if !hasVersion {
		var err error
		modVersion, err = defaultBranch(ctx, dag, ref.Git.CloneURL)
		if err != nil {
			return nil, fmt.Errorf("determine default branch: %w", err)
		}
	}

	gitCommit, err := resolveGitRef(ctx, dag, ref.Git.CloneURL, modVersion)
	if err != nil {
		return nil, fmt.Errorf("resolve git ref: %w", err)
	}

	ref.Version = gitCommit    // TODO preserve semver here
	ref.Git.Commit = gitCommit // but tell the truth here

	if len(segments) == 4 {
		ref.SubPath = segments[3]
		ref.Git.HTMLURL = "https://" + segments[0] + "/" + segments[1] + "/" + segments[2] + "/tree/" + ref.Version + "/" + ref.SubPath
	} else {
		ref.Git.HTMLURL = "https://" + segments[0] + "/" + segments[1] + "/" + segments[2]
	}

	return ref, nil
}

func ResolveModuleDependency(ctx context.Context, dag *dagger.Client, parent *ModuleRef, urlStr string) (*ModuleRef, error) {
	mod, err := ResolveMovingRef(ctx, dag, urlStr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve module: %w", err)
	}

	if mod.Local {
		// make local modules relative to the parent module
		cp := *parent
		if cp.SubPath != "" {
			cp.SubPath = filepath.Join(cp.SubPath, mod.Path)
		} else {
			cp.SubPath = mod.Path
		}
		return &cp, nil
	}

	return mod, nil
}

func defaultBranch(ctx context.Context, dag *dagger.Client, repo string) (string, error) {
	output, err := dag.Container().
		From("alpine/git").
		WithExec([]string{"git", "ls-remote", "--symref", repo, "HEAD"}, dagger.ContainerWithExecOpts{
			SkipEntrypoint: true,
		}).
		Stdout(ctx)
	if err != nil {
		return "", err
	}

	scanner := bufio.NewScanner(bytes.NewBufferString(output))

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}

		if fields[0] == "ref:" && fields[2] == "HEAD" {
			return strings.TrimPrefix(fields[1], "refs/heads/"), nil
		}
	}

	return "", fmt.Errorf("could not deduce default branch from output:\n%s", output)
}

func resolveGitRef(ctx context.Context, dag *dagger.Client, repo, ref string) (string, error) {
	repoDir := dag.Git(repo, dagger.GitOpts{KeepGitDir: true}).Commit(ref).Tree()

	output, err := dag.Container().
		From("alpine/git").
		WithMountedDirectory("/repo", repoDir).
		WithWorkdir("/repo").
		WithExec([]string{"git", "rev-parse", "HEAD"}, dagger.ContainerWithExecOpts{
			SkipEntrypoint: true,
		}).
		Stdout(ctx)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(output), nil
}
