package modules

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"regexp"

	"dagger.io/dagger"
	"github.com/moby/buildkit/identity"
)

var (
	// The digest-pinned ref of an address that can run 'git'
	// FIXME: make this image smaller
	gitImageRef = "index.docker.io/alpine/git@sha256:1031f50b5bdda7eee6167e362cff09b8c809889cd43e5306abc5bf6438e68890"
)

// Ref contains all of the information we're able to learn about a provided
// module ref.
type Ref struct {
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

func (ref *Ref) String() string {
	if ref.Local {
		p, err := ref.LocalSourcePath()
		if err != nil {
			// should be impossible given the ref.Local guard
			panic(err)
		}
		return p
	}

	// don't include subpath, this is a git ref and Path already has any subpath included
	// when set by ResolveMovingRef (which is confusing, needs a refactor)
	if ref.Version == "" {
		return ref.Path
	}
	return fmt.Sprintf("%s@%s", ref.Path, ref.Version)
}

func (ref *Ref) Symbolic() string {
	var root string
	switch {
	case ref.Local:
		root = ref.Path
	case ref.Git != nil:
		root = ref.Git.CloneURL
	default:
		panic("invalid module ref")
	}
	return filepath.Join(root, ref.SubPath)
}

func (ref *Ref) LocalSourcePath() (string, error) {
	if ref.Local {
		// TODO(vito): This may be worth a rethink, but the idea is for local
		// modules to be represented as a 'subpath' of their outer module, that way
		// they can do things like refer to sibling modules at ../foo. But this
		// hasn't been proved out. Anyway, at this layer we need to preserve the
		// subpath because this gets printed to `dagger.json`, and without this the
		// module will depend on itself, leading to an infinite loop.
		return filepath.Join(ref.Path, ref.SubPath), nil
	}
	return "", fmt.Errorf("cannot get local source path for non-local module")
}

func (ref *Ref) Config(ctx context.Context, c *dagger.Client) (*Config, error) {
	switch {
	case ref.Local:
		configBytes, err := os.ReadFile(filepath.Join(ref.Path, Filename))
		if err != nil {
			return nil, fmt.Errorf("failed to read local config file: %w", err)
		}
		var cfg Config
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
			configPath = filepath.Join(ref.SubPath, Filename)
		} else {
			configPath = Filename
		}
		configStr, err := repoDir.File(configPath).Contents(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to read git config file: %w", err)
		}
		var cfg Config
		if err := json.Unmarshal([]byte(configStr), &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse git config file: %w", err)
		}
		return &cfg, nil

	default:
		panic("invalid module ref")
	}
}

func (ref *Ref) AsModule(ctx context.Context, c *dagger.Client) (*dagger.Module, error) {
	src, subPath, err := ref.source(ctx, c)
	if err != nil {
		return nil, err
	}

	return src.AsModule(dagger.DirectoryAsModuleOpts{
		SourceSubpath: subPath,
	}), nil
}

func (ref *Ref) AsUninitializedModule(ctx context.Context, c *dagger.Client) (*dagger.Module, error) {
	src, subPath, err := ref.source(ctx, c)
	if err != nil {
		return nil, err
	}

	return c.Module().
		WithSource(src, dagger.ModuleWithSourceOpts{
			Subpath: subPath,
		}), nil
}

func (ref *Ref) source(ctx context.Context, c *dagger.Client) (*dagger.Directory, string, error) {
	cfg, err := ref.Config(ctx, c)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get module config: %w", err)
	}

	switch {
	case ref.Local:
		localSrc, err := ref.LocalSourcePath()
		if err != nil {
			// should be impossible given the ref.Local guard
			panic(err)
		}
		localSrc, err = filepath.Abs(localSrc)
		if err != nil {
			panic(err)
		}

		modRootDir, subdirRelPath, err := cfg.RootAndSubpath(localSrc)
		if err != nil {
			return nil, "", fmt.Errorf("failed to get module root: %w", err)
		}

		return c.Host().
				Directory(modRootDir, dagger.HostDirectoryOpts{
					Include: cfg.Include,
					Exclude: cfg.Exclude,
				}),
			subdirRelPath, nil

	case ref.Git != nil:
		rootPath, relSubPath, err := cfg.RootAndSubpath(ref.SubPath)
		if err != nil {
			return nil, "", fmt.Errorf("failed to get module root: %w", err)
		}

		return c.Git(ref.Git.CloneURL).Commit(ref.Version).Tree().
				Directory(rootPath),
			relSubPath, nil
	default:
		return nil, "", fmt.Errorf("invalid module (local=%t, git=%t)", ref.Local, ref.Git != nil)
	}
}

// TODO dedup with ResolveMovingRef
func ResolveStableRef(modQuery string) (*Ref, error) {
	modPath, modVersion, hasVersion := strings.Cut(modQuery, "@")

	ref := &Ref{
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

func ResolveMovingRef(ctx context.Context, dag *dagger.Client, modQuery string) (*Ref, error) {
	modPath, modVersion, hasVersion := strings.Cut(modQuery, "@")

	ref := &Ref{
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

	if hasVersion && isSemver(modVersion) {
		allTags, err := gitTags(ctx, dag, ref.Git.CloneURL)
		if err != nil {
			return nil, fmt.Errorf("get git tags: %w", err)
		}
		matched, err := matchVersion(allTags, modVersion, ref.SubPath)
		if err != nil {
			return nil, fmt.Errorf("matching version to tags: %w", err)
		}
		// reassign modVersion to matched tag which could be subPath/tag
		modVersion = matched
	}

	gitCommit, err := dag.Git(ref.Git.CloneURL, dagger.GitOpts{KeepGitDir: true}).Commit(modVersion).Commit(ctx)
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

func ResolveModuleDependency(ctx context.Context, dag *dagger.Client, parent *Ref, urlStr string) (*Ref, error) {
	mod, err := ResolveMovingRef(ctx, dag, urlStr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve module: %w", err)
	}

	if !mod.Local {
		return mod, nil
	}

	// make local modules relative to the parent module
	cp := *parent

	if cp.Local {
		cp.SubPath = filepath.Join(cp.SubPath, mod.Path)
	} else {
		// the parent is a git module, in which case both Path and SubPath include the full
		// path to the module, so we need to set both (this is confusing and needs a larger refactor)
		cp.SubPath = filepath.Join(cp.SubPath, mod.Path)
		cp.Path = filepath.Join(cp.Path, mod.Path)
	}

	return &cp, nil
}

func defaultBranch(ctx context.Context, dag *dagger.Client, repo string) (string, error) {
	output, err := dag.Container().
		From(gitImageRef).
		WithEnvVariable("CACHEBUSTER", identity.NewID()). // force this to always run so we don't get stale data
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

// find all git tags for a given repo
func gitTags(ctx context.Context, dag *dagger.Client, repo string) ([]string, error) {
	output, err := dag.Container().
		From(gitImageRef).
		WithEnvVariable("CACHEBUSTER", identity.NewID()). // force this to always run so we don't get stale data
		WithExec([]string{"git", "ls-remote", "--tags", "--symref", repo}, dagger.ContainerWithExecOpts{
			SkipEntrypoint: true,
		}).
		Stdout(ctx)
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(bytes.NewBufferString(output))

	tags := []string{}
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}

		tags = append(tags, strings.TrimPrefix(fields[1], "refs/tags/"))
	}

	return tags, nil
}

func isSemver(ver string) bool {
	re := regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	return re.MatchString(ver)
}

// Match a version string in a list of versions with optional subPath
// e.g. github.com/foo/daggerverse/mod@mod/v1.0.0
// e.g. github.com/foo/mod@v1.0.0
// TODO smarter matching logic, e.g. v1 == v1.0.0
func matchVersion(versions []string, match, subPath string) (string, error) {
	// If theres a subPath, first match on {subPath}/{match} for monorepo tags
	if subPath != "" {
		matched, err := matchVersion(versions, fmt.Sprintf("%s/%s", subPath, match), "")
		// no error means there's a match with subpath/match
		if err == nil {
			return matched, nil
		}
	}

	for _, v := range versions {
		if v == match {
			return v, nil
		}
	}
	return "", fmt.Errorf("unable to find version %s", match)
}
