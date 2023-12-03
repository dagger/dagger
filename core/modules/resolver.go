package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"dagger.io/dagger"
	"github.com/Masterminds/semver/v3"
	"golang.org/x/exp/slog"
)

// Ref contains all of the information we're able to learn about a provided
// module ref.
type Ref struct {
	// Source determines how the module source code is fetched.
	Source

	// Tag is a human-readable identifier for the module version.
	//
	// The interpretation of this value is up to the source type. For Git this is
	// a ref, meaning it may be a branch name _or_ a tag name.
	//
	// Semver tags are given special treatment, again depending on the source's
	// interpretation. For Git the tag will be affixed to the subpath to support
	// semver within a monorepo to form a tag like sub/dir/v1.2.3.
	Tag string

	// Hash is a hash of the content, if any.
	//
	// The interpretation of this value is up to the source type. For Git this is
	// a commit hash. For HTTP this is a digest of the content, e.g.
	// sha256:<hash>.
	Hash string
}

// Source identifies a location from which a module's source code may be
// fetched. It is a union type; only one field must be present.
type Source struct {
	Local *LocalSource
	Git   *GitSource
}

func (ref *Ref) IsMoving() bool {
	return ref.Hash == ""
}

func (ref *Ref) IsPinned() bool {
	return ref.Hash != ""
}

func (ref *Ref) Pin(ctx context.Context, dag *dagger.Client) error {
	tag, hash, err := ref.Source.Pin(ctx, dag)
	if err != nil {
		return fmt.Errorf("refresh source: %w", err)
	}
	ref.Tag = tag
	ref.Hash = hash
	return nil
}

func (source Source) Join(relpath string) Source {
	switch {
	case source.Local != nil:
		local := *source.Local
		local.Path = path.Join(source.Local.Path, relpath)
		source.Local = &local
	case source.Git != nil:
		git := *source.Git
		git.Dir = path.Join(source.Git.Dir, relpath)
		source.Git = &git
	default:
		panic("invalid module source")
	}
	return source
}

func (source *Source) String() string {
	switch {
	case source.Local != nil:
		return source.Local.Path
	case source.Git != nil:
		cp := *source.Git.CloneURL
		cp.Scheme = "git"
		if source.Git.Dir != "" {
			cp.Path += "//" + source.Git.Dir
		}
		return cp.String()
	default:
		panic("invalid module source")
	}
}

func (source *Source) Pin(ctx context.Context, dag *dagger.Client) (string, string, error) {
	switch {
	case source.Local != nil:
		// local paths are never versioned
		return "", "", nil
	case source.Git != nil:
		repo := dag.Git(source.Git.CloneURL.String())

		var err error

		ref := source.Git.Ref
		commit := source.Git.Commit

		if ref == "" {
			if commit != "" {
				// if we have a commit already, who knows what the appropriate ref is
			} else {
				versions, err := source.Git.Versions(ctx, dag)
				if err != nil {
					return "", "", fmt.Errorf("list versions: %w", err)
				}
				if len(versions) == 0 {
					ref, err = repo.DefaultBranch(ctx)
					if err != nil {
						return "", "", fmt.Errorf("determine default branch: %w", err)
					}
				} else {
					ref = versions[len(versions)-1].Original()
					log.Println("!!! LATEST VERSION", ref)
				}
			}
		}

		if ver, err := semver.NewVersion(ref); err == nil {
			// TODO: this is wrong, want v1 to match v1.*, v1.2 to match v1.2.*, and
			// v1.2.3 to mean exactly v1.2.3. using ~ will allow v1.2.4+.
			constraint, err := semver.NewConstraint("~" + ver.Original())
			if err != nil {
				return "", "", fmt.Errorf("parse semver constraint: %w", err)
			}
			log.Println("CONSTRAINT", constraint)
			versions, err := source.Git.Versions(ctx, dag)
			if err != nil {
				return "", "", fmt.Errorf("list versions: %w", err)
			}
			for i := len(versions) - 1; i >= 0; i-- {
				if constraint.Check(versions[i]) {
					ref = versions[i].Original()
					break
				}
			}
		}

		tag := ref

		if source.Git.Dir != "" {
			tag = path.Join(source.Git.Dir, tag)
			log.Println("!!! REF", ref)
		}

		if commit == "" {
			commit, err = repo.Tag(tag).Commit(ctx)
			if err != nil {
				return "", "", fmt.Errorf("resolve git ref: %w", err)
			}
		}

		source.Git.Ref = tag
		source.Git.Commit = commit

		return ref, // leave the ref non-dir-scoped
			commit, nil
	default:
		panic("invalid module source")
	}
}

type LocalSource struct {
	// Path is the local path to the source.
	Path string
}

type GitSource struct {
	// CloneURL is the URL to clone.
	CloneURL *url.URL

	// Ref is the ref to check out.
	Ref string

	// Commit is the commit to check out.
	Commit string

	// Dir is the subdirectory containing the module, if any.
	Dir string
}

func (source *GitSource) Versions(ctx context.Context, dag *dagger.Client) ([]*semver.Version, error) {
	repo := dag.Git(source.CloneURL.String())

	pattern := "v*"
	if source.Dir != "" {
		// scope semver tags to the subdir
		pattern = source.Dir + "/" + pattern
	}

	tags, err := repo.Tags(ctx, dagger.GitRepositoryTagsOpts{
		Patterns: []string{"refs/tags/" + pattern},
	})
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}

	versions := []*semver.Version{}
	for _, tag := range tags {
		if source.Dir != "" {
			tag = strings.TrimPrefix(tag, source.Dir+"/")
		}
		ver, err := semver.NewVersion(tag)
		if err != nil {
			// TODO should probably just remove this, mostly for debugging
			slog.Warn("ignoring invalid semver tag %q: %s", tag, err)
			continue
		}
		versions = append(versions, ver)
	}
	sort.Sort(semver.Collection(versions))

	return versions, nil
}

func ParseRef(ref string) (*Ref, error) {
	u, err := url.Parse(ref)
	if err != nil {
		return nil, fmt.Errorf("parse ref: %w", err)
	}
	switch u.Scheme {
	case "":
		switch {
		case u.Path == ".",
			u.Path == "..",
			strings.HasPrefix(u.Path, "./"),
			strings.HasPrefix(u.Path, "../"),
			strings.HasPrefix(u.Path, "/"):
			return parseLocalRef(u)
		default:
			// guess that this is a git URL
			return ParseRef("git://" + u.Path)
		}
	case "git":
		return parseGitRef(u)
	case "gh":
		return parseGHRef(u)
	// case "http":
	default:
		return nil, fmt.Errorf("unsupported ref scheme: %q", u.Scheme)
	}
}

// String returns the canonical representation of the ref.
func (ref *Ref) String() string {
	str := ref.Symbolic()

	if ref.Tag != "" {
		// XXX(vito): decide if this is Symbol.
		str += ":" + ref.Tag
	}

	if ref.Hash != "" {
		str += "@" + ref.Hash
	}

	return str
}

// Symbolic returns the symbolic representation of the ref, without the hash or
// tag.
//
// This is used by `dagger mod install` to determine whether to add or replace
// a dependency.
func (ref *Ref) Symbolic() string {
	return ref.Source.String()
}

// LocalSourcePath returns the path of the module's source code on the local
// filesystem. If the ref is not local, it returns an error.
func (ref *Ref) LocalSourcePath() (string, error) {
	if ref.Local != nil {
		return ref.Local.Path, nil
	}
	return "", fmt.Errorf("cannot get local source path for non-local module")
}

// Config returns the module's config.
func (ref *Ref) Config(ctx context.Context, dag *dagger.Client) (*Config, error) {
	if !ref.IsPinned() {
		if err := ref.Pin(ctx, dag); err != nil {
			return nil, err
		}
	}

	switch {
	case ref.Local != nil:
		configBytes, err := os.ReadFile(filepath.Join(ref.Local.Path, Filename))
		if err != nil {
			return nil, fmt.Errorf("failed to read local config file: %w", err)
		}
		var cfg Config
		if err := json.Unmarshal(configBytes, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse local config file: %w", err)
		}
		return &cfg, nil

	case ref.Git != nil:
		if dag == nil {
			return nil, fmt.Errorf("cannot load git module config with nil dagger client")
		}
		repoDir := dag.Git(ref.Git.CloneURL.String()).Commit(ref.Git.Commit).Tree()
		var configPath string
		if ref.Git.Dir != "" {
			// NB: we use path and not filepath here because we always assume the
			// Dagger API follows Unix conventions, or at the very least we don't
			// want to assume the host's path convention matches it.
			configPath = path.Join(ref.Git.Dir, Filename)
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
	cfg, err := ref.Config(ctx, c)
	if err != nil {
		return nil, fmt.Errorf("failed to get module config: %w", err)
	}

	switch {
	case ref.Local != nil:
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
			return nil, fmt.Errorf("failed to get module root: %w", err)
		}
		return c.Host().Directory(modRootDir, dagger.HostDirectoryOpts{
			Include: cfg.Include,
			Exclude: cfg.Exclude,
		}).AsModule(dagger.DirectoryAsModuleOpts{
			SourceSubpath: subdirRelPath,
		}), nil

	case ref.Git != nil:
		rootPath, relSubPath, err := cfg.RootAndSubpath(ref.Git.Dir)
		if err != nil {
			return nil, fmt.Errorf("failed to get module root: %w", err)
		}
		if ref.Git.Commit != "" {
			return nil, fmt.Errorf("missing commit hash for git ref")
		}
		return c.Git(ref.Git.CloneURL.String()).Commit(ref.Git.Commit).Tree().
			Directory(rootPath).
			AsModule(dagger.DirectoryAsModuleOpts{SourceSubpath: relSubPath}), nil

	default:
		return nil, fmt.Errorf("invalid module: %+v", ref)
	}
}

//// TODO dedup with ResolveMovingRef
//func ResolveStableRef(modQuery string) (*Ref, error) {
//	modPath, modVersion, hasVersion := strings.Cut(modQuery, "@")

//	ref := &Ref{
//		Path: modPath,
//	}

//	// TODO: figure out how to support arbitrary repos in a predictable way.
//	// Maybe piggyback on whatever Go supports? (the whole <meta go-import>
//	// thing)
//	isGitHub := strings.HasPrefix(modPath, "github.com/")

//	if !hasVersion {
//		if isGitHub {
//			return nil, fmt.Errorf("no version provided for remote ref: %s", modQuery)
//		}

//		// assume local path
//		//
//		// NB(vito): HTTP URLs should be supported by taking a sha256 digest as the
//		// version. so it should be safe to assume no version = local path. as a
//		// rule, if it's local we don't need to version it; if it's remote, we do.
//		ref.Local = true
//		return ref, nil
//	}

//	ref.Git = &GitRef{} // assume git for now, HTTP can come later

//	if !isGitHub {
//		return nil, fmt.Errorf("for now, only github.com/ paths are supported: %s", modPath)
//	}

//	segments := strings.SplitN(modPath, "/", 4)
//	if len(segments) < 3 {
//		return nil, fmt.Errorf("invalid github.com path: %s", modPath)
//	}

//	ref.Git.CloneURL = "https://" + segments[0] + "/" + segments[1] + "/" + segments[2]

//	if !hasVersion {
//		return nil, fmt.Errorf("no version provided for %s", modPath)
//	}

//	ref.Version = modVersion    // assume commit
//	ref.Git.Commit = modVersion // assume commit

//	if len(segments) == 4 {
//		ref.SubPath = segments[3]
//		ref.Git.HTMLURL = "https://" + segments[0] + "/" + segments[1] + "/" + segments[2] + "/tree/" + ref.Version + "/" + ref.SubPath
//	} else {
//		ref.Git.HTMLURL = "https://" + segments[0] + "/" + segments[1] + "/" + segments[2]
//	}

//	return ref, nil
//}

//func ResolveMovingRef(ctx context.Context, dag *dagger.Client, modQuery string) (*Ref, error) {
//	modPath, modVersion, hasVersion := strings.Cut(modQuery, "@")

//	ref := &Ref{
//		Path: modPath,
//	}

//	// TODO: figure out how to support arbitrary repos in a predictable way.
//	// Maybe piggyback on whatever Go supports? (the whole <meta go-import>
//	// thing)
//	isGitHub := strings.HasPrefix(modPath, "github.com/")

//	if !hasVersion && !isGitHub {
//		// assume local path
//		//
//		// NB(vito): HTTP URLs should be supported by taking a sha256 digest as the
//		// version. so it should be safe to assume no version = local path. as a
//		// rule, if it's local we don't need to version it; if it's remote, we do.
//		ref.Local = true
//		return ref, nil
//	}

//	ref.Git = &GitRef{} // assume git for now, HTTP can come later

//	if !isGitHub {
//		return nil, fmt.Errorf("for now, only github.com/ paths are supported: %q", modQuery)
//	}

//	segments := strings.SplitN(modPath, "/", 4)
//	if len(segments) < 3 {
//		return nil, fmt.Errorf("invalid github.com path: %s", modPath)
//	}

//	ref.Git.CloneURL = "https://" + segments[0] + "/" + segments[1] + "/" + segments[2]

//	if !hasVersion {
//		var err error
//		modVersion, err = defaultBranch(ctx, dag, ref.Git.CloneURL)
//		if err != nil {
//			return nil, fmt.Errorf("determine default branch: %w", err)
//		}
//	}

//	gitCommit, err := dag.Git(ref.Git.CloneURL, dagger.GitOpts{KeepGitDir: true}).Commit(modVersion).Commit(ctx)
//	if err != nil {
//		return nil, fmt.Errorf("resolve git ref: %w", err)
//	}

//	ref.Version = gitCommit    // TODO preserve semver here
//	ref.Git.Commit = gitCommit // but tell the truth here

//	if len(segments) == 4 {
//		ref.SubPath = segments[3]
//		ref.Git.HTMLURL = "https://" + segments[0] + "/" + segments[1] + "/" + segments[2] + "/tree/" + ref.Version + "/" + ref.SubPath
//	} else {
//		ref.Git.HTMLURL = "https://" + segments[0] + "/" + segments[1] + "/" + segments[2]
//	}

//	return ref, nil
//}

func (depRef *Ref) ParseDependency(dep string) (*Ref, error) {
	depRef, err := ParseRef(dep)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve module: %w", err)
	}

	if depRef.Local == nil {
		return depRef, nil
	}

	// WEIRD: in a monorepo, there are semver tags associated to each subdir.
	// But we can have apko/v1.2.3 depend on ../git which technically needs to be
	// referred to by some bastardization like gh://vito//git:apko/v1.2.3@deadbeef.
	cp := *depRef
	cp.Source = depRef.Join(depRef.Local.Path)
	return &cp, nil
}

//func defaultBranch(ctx context.Context, dag *dagger.Client, repo string) (string, error) {
//	output, err := dag.Container().
//		From("alpine/git").
//		WithEnvVariable("CACHEBUSTER", identity.NewID()). // force this to always run so we don't get stale data
//		WithExec([]string{"git", "ls-remote", "--symref", repo, "HEAD"}, dagger.ContainerWithExecOpts{
//			SkipEntrypoint: true,
//		}).
//		Stdout(ctx)
//	if err != nil {
//		return "", err
//	}

//	scanner := bufio.NewScanner(bytes.NewBufferString(output))

//	for scanner.Scan() {
//		fields := strings.Fields(scanner.Text())
//		if len(fields) < 3 {
//			continue
//		}

//		if fields[0] == "ref:" && fields[2] == "HEAD" {
//			return strings.TrimPrefix(fields[1], "refs/heads/"), nil
//		}
//	}

//	return "", fmt.Errorf("could not deduce default branch from output:\n%s", output)
//}

func parseLocalRef(u *url.URL) (*Ref, error) {
	localPath, dir, tag, hash := parseDirTagHash(u.Path)
	if tag != "" {
		return nil, fmt.Errorf("tags are not supported for local modules")
	}
	if hash != "" {
		return nil, fmt.Errorf("hashes are not supported for local modules")
	}
	return &Ref{
		Source: Source{
			Local: &LocalSource{
				Path: path.Join(localPath, dir),
			},
		},
	}, nil
}

func parseGitRef(u *url.URL) (*Ref, error) {
	if u.Host == "" {
		return nil, fmt.Errorf("missing host in git URL")
	}
	rest, dir, tag, hash := parseDirTagHash(u.Path)
	if u.Host == "github.com" && dir == "" {
		// BACKWARDS-COMPAT: for github.com, assume the first two segments are
		// user/repo and the rest is the subdir.
		//
		// XXX(vito): consider sunsetting this ASAP as it's strongly preferable to
		// not couple to particular domains.
		segments := strings.SplitN(rest, "/", 3) // ["user", "repo", "sub/dir"]
		if len(segments) == 3 {
			dir = path.Clean(segments[2])          // "sub/dir"
			rest = strings.Join(segments[:2], "/") // "/user/repo"
		}
	}
	return &Ref{
		Source: Source{
			Git: &GitSource{
				CloneURL: &url.URL{
					Scheme: "https",
					Host:   u.Host,
					Path:   "/" + rest,
				},
				Ref:    tag,
				Commit: hash,
				Dir:    dir,
			},
		},
		Tag:  tag,
		Hash: hash,
	}, nil
}

func parseGHRef(u *url.URL) (*Ref, error) {
	rest, dir, tag, hash := parseDirTagHash(u.Path)
	user, repo := u.Host, rest
	if strings.Contains(repo, "/") {
		return nil, fmt.Errorf("gh:// format has extra path after repo: %q - must be gh://USER[/REPO][//SUBDIR]", repo)
	}
	if repo == "" {
		repo = "daggerverse"
	}
	return &Ref{
		Source: Source{
			Git: &GitSource{
				CloneURL: &url.URL{
					Scheme: "https",
					Host:   "github.com",
					Path:   path.Join("/", user, repo),
				},
				Ref:    tag,
				Commit: hash,
				Dir:    dir,
			},
		},
		Tag:  tag,
		Hash: hash,
	}, nil
}

func parseDirTagHash(str string) (rest, dir, tag, hash string) {
	var ok bool
	rest, hash, ok = strings.Cut(str, "@")
	if ok {
		str = rest
	}
	rest, tag, ok = strings.Cut(str, ":")
	if ok {
		str = rest
	}
	rest, dir, ok = strings.Cut(str, "//")
	if ok {
		dir = path.Clean(dir)
	}
	rest = strings.TrimPrefix(rest, "/")
	return
}
