package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	containerdfs "github.com/containerd/continuity/fs"
	bkcache "github.com/moby/buildkit/cache"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	bksession "github.com/moby/buildkit/session"
	"github.com/moby/buildkit/snapshot"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/sys/user"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/mod/semver"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"

	"github.com/dagger/dagger/core/reffs"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/slog"
)

var (
	errEmptyResultRef = fmt.Errorf("empty result reference")
)

type Evaluatable interface {
	dagql.Typed
	Evaluate(context.Context) (*buildkit.Result, error)
}

type HasPBDefinitions interface {
	// PBDefinitions returns all the buildkit definitions that are part of a core type
	PBDefinitions(context.Context) ([]*pb.Definition, error)
}

func collectPBDefinitions(ctx context.Context, value dagql.Typed) ([]*pb.Definition, error) {
	switch x := value.(type) {
	case dagql.String, dagql.Int, dagql.Boolean, dagql.Float:
		// nothing to do
		return nil, nil
	case dagql.Enumerable: // dagql.Array
		defs := []*pb.Definition{}
		for i := 1; i < x.Len(); i++ {
			val, err := x.Nth(i)
			if err != nil {
				return nil, fmt.Errorf("failed to get nth value: %w", err)
			}
			elemDefs, err := collectPBDefinitions(ctx, val)
			if err != nil {
				return nil, fmt.Errorf("failed to link nth value dependency blobs: %w", err)
			}
			defs = append(defs, elemDefs...)
		}
		return defs, nil
	case dagql.Derefable: // dagql.Nullable
		if inner, ok := x.Deref(); ok {
			return collectPBDefinitions(ctx, inner)
		} else {
			return nil, nil
		}
	case dagql.Wrapper: // dagql.Result
		return collectPBDefinitions(ctx, x.Unwrap())
	case HasPBDefinitions:
		return x.PBDefinitions(ctx)
	default:
		// NB: being SUPER cautious for now, since this feels a bit spooky to drop
		// on the floor. might be worth just implementing HasPBDefinitions for
		// everything. (would be nice to just skip scalars though.)
		slog.Warn("collectPBDefinitions: unhandled type", "type", fmt.Sprintf("%T", value))
		return nil, nil
	}
}

type Digestable interface {
	// Digest returns a content-digest of an object.
	Digest() (digest.Digest, error)
}

func DigestOf(v any) (digest.Digest, error) {
	if v, ok := v.(Digestable); ok {
		return v.Digest()
	}

	vs, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return digest.FromBytes(vs), nil
}

func absPath(workDir string, containerPath string) string {
	if path.IsAbs(containerPath) {
		return containerPath
	}

	if workDir == "" {
		workDir = "/"
	}

	return path.Join(workDir, containerPath)
}

func defToState(def *pb.Definition) (llb.State, error) {
	if def == nil || def.Def == nil {
		// NB(vito): llb.Scratch().Marshal().ToPB() produces an empty
		// *pb.Definition. If we don't convert it properly back to a llb.Scratch()
		// we'll hit 'cannot marshal empty definition op' when trying to marshal it
		// again.
		return llb.Scratch(), nil
	}

	defop, err := llb.NewDefinitionOp(def)
	if err != nil {
		return llb.State{}, err
	}

	return llb.NewState(defop), nil
}

func resolveUIDGID(ctx context.Context, fsSt llb.State, bk *buildkit.Client, platform Platform, owner string) (*Ownership, error) {
	uidOrName, gidOrName, hasGroup := strings.Cut(owner, ":")

	var uid, gid int
	var uname, gname string

	uid, err := parseUID(uidOrName)
	if err != nil {
		uname = uidOrName
	}

	if hasGroup {
		gid, err = parseUID(gidOrName)
		if err != nil {
			gname = gidOrName
		}
	}

	var fs fs.FS
	if uname != "" || gname != "" {
		fs, err = reffs.OpenState(ctx, bk, fsSt, llb.Platform(platform.Spec()))
		if err != nil {
			return nil, fmt.Errorf("open fs state for name->id: %w", err)
		}
	}

	if uname != "" {
		uid, err = findUID(fs, uname)
		if err != nil {
			return nil, fmt.Errorf("find uid: %w", err)
		}
	}

	if gname != "" {
		gid, err = findGID(fs, gname)
		if err != nil {
			return nil, fmt.Errorf("find gid: %w", err)
		}
	}

	if !hasGroup {
		gid = uid
	}

	return &Ownership{uid, gid}, nil
}

func findUID(fs fs.FS, uname string) (int, error) {
	f, err := fs.Open("/etc/passwd")
	if err != nil {
		return -1, fmt.Errorf("open /etc/passwd: %w", err)
	}

	users, err := user.ParsePasswdFilter(f, func(u user.User) bool {
		return u.Name == uname
	})
	if err != nil {
		return -1, fmt.Errorf("parse /etc/passwd: %w", err)
	}

	if len(users) == 0 {
		return -1, fmt.Errorf("no such user: %s", uname)
	}

	return users[0].Uid, nil
}

func findGID(fs fs.FS, gname string) (int, error) {
	f, err := fs.Open("/etc/group")
	if err != nil {
		return -1, fmt.Errorf("open /etc/passwd: %w", err)
	}

	groups, err := user.ParseGroupFilter(f, func(g user.Group) bool {
		return g.Name == gname
	})
	if err != nil {
		return -1, fmt.Errorf("parse /etc/group: %w", err)
	}

	if len(groups) == 0 {
		return -1, fmt.Errorf("no such group: %s", gname)
	}

	return groups[0].Gid, nil
}

// NB: from Buildkit
func parseUID(str string) (int, error) {
	if str == "root" {
		return 0, nil
	}
	uid, err := strconv.ParseInt(str, 10, 32)
	if err != nil {
		return 0, err
	}
	return int(uid), nil
}

// AddEnv adds or updates an environment variable in 'env'.
func AddEnv(env []string, name, value string) []string {
	// Implementation from the dockerfile2llb project.
	gotOne := false

	for i, envVar := range env {
		k, _, _ := strings.Cut(envVar, "=")
		if shell.EqualEnvKeys(k, name) {
			env[i] = fmt.Sprintf("%s=%s", name, value)
			gotOne = true
			break
		}
	}

	if !gotOne {
		env = append(env, fmt.Sprintf("%s=%s", name, value))
	}

	return env
}

// LookupEnv returns the value of an environment variable.
func LookupEnv(env []string, name string) (string, bool) {
	for _, envVar := range env {
		k, v, _ := strings.Cut(envVar, "=")
		if shell.EqualEnvKeys(k, name) {
			return v, true
		}
	}
	return "", false
}

// WalkEnv iterates over all environment variables with parsed
// key and value, and original string.
func WalkEnv(env []string, fn func(string, string, string)) {
	for _, envVar := range env {
		key, value, _ := strings.Cut(envVar, "=")
		fn(key, value, envVar)
	}
}

// mergeEnv adds or updates environment variables from 'src' in 'dst'.
func mergeEnv(dst, src []string) []string {
	WalkEnv(src, func(k, v, _ string) {
		dst = AddEnv(dst, k, v)
	})
	return dst
}

// mergeMap adds or updates every key-value pair from the 'src' map
// into the 'dst' map.
func mergeMap[T any](dst, src map[string]T) map[string]T {
	if src == nil {
		return dst
	}

	if dst == nil {
		return src
	}

	maps.Copy(dst, src)

	return dst
}

// mergeImageConfig merges the 'src' image metadata into 'dst'.
//
// Only the configurations that have corresponding `WithXXX` and `WithoutXXX`
// methods in `Container` are added or updated (i.e., `Env`, `Labels` and
// `ExposedPorts`). Everything else is replaced.
func mergeImageConfig(dst, src specs.ImageConfig) specs.ImageConfig {
	res := src

	res.Env = mergeEnv(dst.Env, src.Env)
	res.Labels = mergeMap(dst.Labels, src.Labels)
	res.ExposedPorts = mergeMap(dst.ExposedPorts, src.ExposedPorts)

	return res
}

func ptr[T any](v T) *T {
	return &v
}

// MountRef is a utility for easily mounting a ref
func MountRef(ctx context.Context, ref bkcache.Ref, g bksession.Group, f func(string) error) error {
	mount, err := ref.Mount(ctx, false, g)
	if err != nil {
		return err
	}
	lm := snapshot.LocalMounter(mount)
	defer lm.Unmount()

	dir, err := lm.Mount()
	if err != nil {
		return err
	}
	return f(dir)
}

// mountLLB is a utility for easily mounting an llb definition
func mountLLB(ctx context.Context, llb *pb.Definition, f func(string) error) error {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	bk, err := query.Buildkit(ctx)
	if err != nil {
		return fmt.Errorf("failed to get buildkit client: %w", err)
	}
	res, err := bk.Solve(ctx, bkgw.SolveRequest{
		Definition: llb,
	})
	if err != nil {
		return err
	}

	ref, err := res.SingleRef()
	if err != nil {
		return err
	}
	// empty directory, i.e. llb.Scratch()
	if ref == nil {
		tmp, err := os.MkdirTemp("", "mount")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		return f(tmp)
	}
	return ref.Mount(ctx, f)
}

func Supports(ctx context.Context, minVersion string) (bool, error) {
	id := dagql.CurrentID(ctx)
	return engine.CheckVersionCompatibility(id.View(), minVersion), nil
}

// AllVersion is a view that contains all versions.
var AllVersion = dagql.AllView{}

// AfterVersion is a view that checks if a target version is greater than *or*
// equal to the filtered version.
type AfterVersion string

var _ dagql.ViewFilter = AfterVersion("")

func (minVersion AfterVersion) Contains(version dagql.View) bool {
	if version == "" {
		return true
	}
	return semver.Compare(string(version), string(minVersion)) >= 0
}

// BeforeVersion is a view that checks if a target version is less than the
// filtered version.
type BeforeVersion string

var _ dagql.ViewFilter = BeforeVersion("")

func (maxVersion BeforeVersion) Contains(version dagql.View) bool {
	if version == "" {
		return false
	}
	return semver.Compare(string(version), string(maxVersion)) < 0
}

var (
	enumView = AfterVersion("v0.18.11")
)

// RootPathWithoutFinalSymlink joins a path with a root, evaluating and bounding all
// symlinks except the final component of the path (i.e. the basename component).
// This is useful for the case where one needs to reference a symlink rather than
// following it (e.g. deleting a symlink)
// This function will return an error if any of the symlinks encountered before the final
// path separator reference a location outside of the root path.
func RootPathWithoutFinalSymlink(root, containerPath string) (string, error) {
	linkDir, linkBasename := filepath.Split(containerPath)
	resolvedLinkDir, err := containerdfs.RootPath(root, linkDir)
	if err != nil {
		return "", err
	}
	return path.Join(resolvedLinkDir, linkBasename), nil
}

type execInMountOpt struct {
	commitSnapshot bool
	cacheDesc      string
}

type execInMountOptFn func(opt *execInMountOpt)

func withSavedSnapshot(format string, a ...any) execInMountOptFn {
	return func(opt *execInMountOpt) {
		opt.cacheDesc = fmt.Sprintf(format, a...)
		opt.commitSnapshot = true
	}
}

type fileOrDirectory interface {
	*File | *Directory
	getResult() bkcache.ImmutableRef
	setResult(bkcache.ImmutableRef)
	Evaluatable
}

// execInMount is a helper used by Directory.execInMount and File.execInMount
func execInMount[T fileOrDirectory](ctx context.Context, obj T, f func(string) error, optFns ...execInMountOptFn) (T, error) {
	var saveOpt execInMountOpt
	for _, optFn := range optFns {
		optFn(&saveOpt)
	}

	parentRef, err := getRefOrEvaluate(ctx, obj)
	if err != nil {
		return nil, err
	}

	bkSessionGroup, ok := buildkit.CurrentBuildkitSessionGroup(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit session group in context")
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}

	var mountRef bkcache.Ref
	var newRef bkcache.MutableRef
	if saveOpt.commitSnapshot {
		if saveOpt.cacheDesc == "" {
			return nil, fmt.Errorf("execInMount saveSnapshotOpt missing cache description")
		}
		newRef, err = query.BuildkitCache().New(ctx, parentRef, bkSessionGroup,
			bkcache.WithRecordType(bkclient.UsageRecordTypeRegular), bkcache.WithDescription(saveOpt.cacheDesc))
		if err != nil {
			return nil, err
		}
		mountRef = newRef
	} else {
		if parentRef == nil {
			return nil, errEmptyResultRef
		}
		mountRef = parentRef
	}
	err = MountRef(ctx, mountRef, bkSessionGroup, f)
	if err != nil {
		return nil, err
	}
	if saveOpt.commitSnapshot {
		snap, err := newRef.Commit(ctx)
		if err != nil {
			return nil, err
		}
		obj.setResult(snap)
		return obj, nil
	}
	return obj, nil
}

func getRefOrEvaluate[T fileOrDirectory](ctx context.Context, t T) (bkcache.ImmutableRef, error) {
	ref := t.getResult()
	if ref != nil {
		return ref, nil
	}
	res, err := t.Evaluate(ctx)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	cacheRef, err := res.SingleRef()
	if err != nil {
		return nil, err
	}
	if cacheRef == nil {
		return nil, nil
	}
	return cacheRef.CacheRef(ctx)
}

// Extract the gitignore patterns from its file content and return them as relative paths
// based on the given parent directory.
// The parent directory is required to match the gitignore patterns relative to
// the context directory since the given content may be a .gitignore file from a subdirectory
// of that context.
//
// Example:
//   - contextDir = "/my-project/foo"
//   - .gitignoreDir = "/my-project/foo/bar/.gitignore"
//   - parentDir = "/my-project/foo/bar" -> so **/node_modules becomes my-project/foo/bar/**/node_modules
//
// Pattern additional formatting is based on https://git-scm.com/docs/gitignore so it can
// be correctly applied with further dagger include/exclude filter patterns.
//
// Here are the rules of that filter/formatting:
//
//   - We ignore empty lines and comments (starting with `#`) (`\#` isn't ignored).
//
//   - If there is a separator at the beginning or middle (or both) of the pattern, then the pattern is relative.
//     Otherwise, the pattern is recursive.
//     If pattern is already starting with **, not change needed.
//     If the pattern starts with *, it's not recursive but only matches the directory itself.
//     Example: foo/bar stays foo/bar but foo becomes **/foo
//
//   - If a pattern is negative exclusion (starts with `!`) or targets directory only
//     (ends with `/`), we treat is as a regular path then read the exclusion to make
//     sure the recusive pattern is applied if needed.
//     Example: !foo becomes foo then **/foo then !**/foo
func parseGitIgnore(gitIgnoreContent string, parentDir string) []string {
	ignorePatterns := []string{}

	// Split gitignore files by line
	ignorePatternsLines := strings.Split(gitIgnoreContent, "\n")

	for _, linePattern := range ignorePatternsLines {
		// ignore comments, negatives and empty lines
		if strings.HasPrefix(linePattern, "#") || linePattern == "" {
			continue
		}

		// Save if the pattern is a directory only or negate so we can work with the path
		// only and reconstruct it later
		isDirOnly := strings.HasSuffix(linePattern, "/")
		isNegate := strings.HasPrefix(linePattern, "!")
		pattern := strings.TrimPrefix(strings.TrimSuffix(linePattern, "/"), "!")

		// Based on https://git-scm.com/docs/gitignore
		// If there is a separator at the beginning or middle (or both) of the pattern, then the pattern is relative.
		// Otherwise, the pattern is recursive.
		// If pattern is already starting with **, not change needed
		// If the pattern starts with *, it's not recursive but only matches the directory itself.
		if !strings.Contains(pattern, "/") &&
			!strings.HasPrefix(pattern, "**") &&
			!strings.HasPrefix(pattern, "*") {
			pattern = "**/" + pattern
		}

		// Rebase the pattern based on the relative path from the context.
		relativePattern := filepath.Join(parentDir, pattern)

		// Reconstruct the pattern with negative or directory only pattern
		if isNegate {
			relativePattern = "!" + relativePattern
		}
		if isDirOnly {
			relativePattern += "/"
		}

		ignorePatterns = append(ignorePatterns, relativePattern)
	}

	return ignorePatterns
}

// Load git ignore patterns in the current directory and all its children
// It assumes that the given `dir` only contains `.gitignore` files and directories that may
// contain `.gitignore` files.
func LoadGitIgnoreInDirectory(ctx context.Context, dir dagql.ObjectResult[*Directory]) ([]string, error) {
	name := dir.Self().Dir

	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get dag server: %w", err)
	}

	var entries dagql.Array[dagql.String]
	err = dag.Select(ctx, dir, &entries,
		dagql.Selector{
			Field: "glob",
			Args: []dagql.NamedInput{
				{Name: "pattern", Value: dagql.String("**/.gitignore")},
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list files in directory %s: %w", name, err)
	}

	result := []string{}
	eg, ctx := errgroup.WithContext(ctx)

	// Mutex to protect the result slice from concurrent writes.
	var resultMu sync.Mutex

	// Limit concurrency to avoid overwhelming the engine
	sem := semaphore.NewWeighted(int64(runtime.NumCPU()))

	for i := 1; i <= entries.Len(); i++ {
		i := i

		if err := sem.Acquire(ctx, 1); err != nil {
			return nil, err
		}

		eg.Go(func() error {
			defer sem.Release(1)

			entry, err := entries.Nth(i)
			if err != nil {
				return fmt.Errorf("failed to get entry %d in directory %s: %w", i, name, err)
			}

			entryValue, ok := dagql.UnwrapAs[dagql.String](entry)
			if !ok {
				return fmt.Errorf("expected string, got %T", entry)
			}

			var content dagql.String
			err = dag.Select(ctx, dir, &content,
				dagql.Selector{
					Field: "file",
					Args: []dagql.NamedInput{
						{Name: "path", Value: entryValue},
					},
				},
				dagql.Selector{
					Field: "contents",
				},
			)
			if err != nil {
				return fmt.Errorf("failed to get file %s: %w", entryValue, err)
			}

			patterns := parseGitIgnore(string(content), filepath.Dir(string(entryValue)))

			resultMu.Lock()
			defer resultMu.Unlock()
			result = append(result, patterns...)

			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, fmt.Errorf("failed to load .gitignore files: %w", err)
	}

	return result, nil
}
