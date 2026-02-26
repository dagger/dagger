package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/containerd/containerd/v2/core/mount"
	containerdfs "github.com/containerd/continuity/fs"
	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/dagger/dagger/internal/buildkit/frontend/dockerfile/shell"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/snapshot"
	"github.com/dagger/dagger/internal/buildkit/util/overlay"
	fscopy "github.com/dagger/dagger/internal/fsutil/copy"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/sys/user"
	"github.com/opencontainers/go-digest"
	"golang.org/x/mod/semver"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/slog"
)

var (
	errEmptyResultRef = fmt.Errorf("empty result reference")
)

type Evaluatable interface {
	dagql.Typed
	Evaluate(context.Context) error
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

func findUID(f io.Reader, uname string) (int, error) {
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

func findGID(f io.Reader, gname string) (int, error) {
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
func mergeImageConfig(dst, src dockerspec.DockerOCIImageConfig) dockerspec.DockerOCIImageConfig {
	res := src

	res.Env = mergeEnv(dst.Env, src.Env)
	res.Labels = mergeMap(dst.Labels, src.Labels)
	res.ExposedPorts = mergeMap(dst.ExposedPorts, src.ExposedPorts)

	var healthcheck dockerspec.HealthcheckConfig
	if src.Healthcheck != nil {
		healthcheck = *src.Healthcheck
		src.Healthcheck = &healthcheck
	} else if dst.Healthcheck != nil {
		healthcheck = *dst.Healthcheck
		src.Healthcheck = &healthcheck
	}

	res.OnBuild = append([]string{}, dst.OnBuild...)
	res.OnBuild = append(res.OnBuild, src.OnBuild...)

	if src.Shell != nil {
		res.Shell = append([]string{}, src.Shell...)
	} else if dst.Shell != nil {
		res.Shell = append([]string{}, dst.Shell...)
	}

	return res
}

func ptr[T any](v T) *T {
	return &v
}

type mountRefOpt struct {
	readOnly bool
}

type mountRefOptFn func(opt *mountRefOpt)

func mountRefAsReadOnly(opt *mountRefOpt) {
	opt.readOnly = true
}

// MountRef is a utility for easily mounting a ref.
//
// To simplify external logic, when the ref is nil, i.e. scratch, the callback
// just receives a tmpdir that gets deleted when the function completes.
func MountRef(ctx context.Context, ref bkcache.Ref, _ bksession.Group, f func(string, *mount.Mount) error, optFns ...mountRefOptFn) error {
	dir, m, closer, err := MountRefCloser(ctx, ref, nil, optFns...)
	if err != nil {
		return err
	}
	err = f(dir, m)
	if err != nil {
		closeErr := closer()
		if closeErr != nil {
			err = errors.Join(err, closeErr)
		}
		return err
	}
	return closer()
}

// MountRefCloser is a utility for mounting a ref.
//
// To simplify external logic, when the ref is nil, i.e. scratch, a tmpdir is created (and deleted when the closer func is called).
//
// NOTE: prefer MountRef where possible, unless finer-grained control of when the directory is unmounted is needed.
func MountRefCloser(ctx context.Context, ref bkcache.Ref, _ bksession.Group, optFns ...mountRefOptFn) (_ string, _ *mount.Mount, _ func() error, rerr error) {
	var opt mountRefOpt
	for _, optFn := range optFns {
		optFn(&opt)
	}

	if ref == nil {
		dir, err := os.MkdirTemp("", "readonly-scratch")
		if err != nil {
			return "", nil, nil, err
		}
		return dir, nil, func() error {
			return os.RemoveAll(dir)
		}, nil
	}
	mountable, err := ref.Mount(ctx, opt.readOnly, nil)
	if err != nil {
		return "", nil, nil, err
	}
	ms, unmount, err := mountable.Mount()
	if err != nil {
		return "", nil, nil, err
	}
	defer func() {
		if rerr != nil {
			rerr = errors.Join(rerr, unmount())
		}
	}()
	if len(ms) == 0 {
		return "", nil, nil, fmt.Errorf("no mounts available from ref")
	}
	m := ms[0]

	lm := snapshot.LocalMounterWithMounts(ms)
	dir, err := lm.Mount()
	if err != nil {
		return "", nil, nil, err
	}
	return dir, &m, func() error {
		err := lm.Unmount()
		err = errors.Join(err, unmount())
		return err
	}, nil
}

func Supports(ctx context.Context, minVersion string) bool {
	return AfterVersion(minVersion).Contains(
		dagql.CurrentID(ctx).View(),
	)
}

// AllVersion is a view that contains all versions.
var AllVersion = dagql.AllView{}

// AfterVersion is a view that checks if a target version is greater than *or*
// equal to the filtered version.
type AfterVersion string

var _ dagql.ViewFilter = AfterVersion("")

func (minVersion AfterVersion) Contains(version call.View) bool {
	if version == "" {
		return true
	}
	return semver.Compare(string(version), string(minVersion)) >= 0
}

// BeforeVersion is a view that checks if a target version is less than the
// filtered version.
type BeforeVersion string

var _ dagql.ViewFilter = BeforeVersion("")

func (maxVersion BeforeVersion) Contains(version call.View) bool {
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

type mountObjOpt struct {
	commitSnapshot bool
	cacheDesc      string
}

type mountObjOptFn func(opt *mountObjOpt)

func withSavedSnapshot(format string, a ...any) mountObjOptFn {
	return func(opt *mountObjOpt) {
		opt.cacheDesc = fmt.Sprintf(format, a...)
		opt.commitSnapshot = true
	}
}

type fileOrDirectory interface {
	*File | *Directory
	getSnapshot(context.Context) (bkcache.ImmutableRef, error)
	setSnapshot(bkcache.ImmutableRef)
}

// mountObj evaluates an object and mounts the root fs and returns the mounted path and a closer, which will unmount
// the file or directory object's root filesystem, and potentially return a modified object, if both the withSavedSnapshot option is specified and the abort flag was not set.
// The abort flag is only used when the withSavedSnapshot option is specified.
// NOTE: prefer execInMount where possible, unless finer-grained control of the filesystem mount is required.
func mountObj[T fileOrDirectory](ctx context.Context, obj T, optFns ...mountObjOptFn) (string, func(abort bool) (T, error), error) {
	var opt mountObjOpt
	for _, optFn := range optFns {
		optFn(&opt)
	}

	var parentRef bkcache.ImmutableRef
	if obj != nil {
		var err error
		parentRef, err = getRefOrEvaluate(ctx, obj)
		if err != nil {
			return "", nil, err
		}
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return "", nil, err
	}

	var mountRef bkcache.Ref
	var newRef bkcache.MutableRef
	if opt.commitSnapshot {
		if opt.cacheDesc == "" {
			return "", nil, fmt.Errorf("mountObj saveSnapshotOpt missing cache description")
		}
		newRef, err = query.BuildkitCache().New(ctx, parentRef, nil,
			bkcache.WithRecordType(bkclient.UsageRecordTypeRegular), bkcache.WithDescription(opt.cacheDesc))
		if err != nil {
			return "", nil, err
		}
		mountRef = newRef
	} else {
		if parentRef == nil {
			return "", nil, errEmptyResultRef
		}
		mountRef = parentRef
	}
	var mountRefOpts []mountRefOptFn
	if !opt.commitSnapshot {
		mountRefOpts = append(mountRefOpts, mountRefAsReadOnly)
	}
	rootPath, _, closer, err := MountRefCloser(ctx, mountRef, nil, mountRefOpts...)
	if err != nil {
		return "", nil, err
	}

	if opt.commitSnapshot {
		return rootPath, func(abort bool) (T, error) {
			err := closer()
			if err != nil {
				return nil, err
			}
			if !abort {
				snap, err := newRef.Commit(ctx)
				if err != nil {
					return nil, err
				}
				obj.setSnapshot(snap)
			}
			return obj, nil
		}, nil
	}

	return rootPath, func(_ bool) (T, error) {
		err := closer()
		if err != nil {
			return nil, err
		}
		return obj, nil
	}, nil
}

// RestoreErrPath will restore the path of an error, which is useful for both removing buildkit mount root paths and referencing uncleaned paths
// Note: TrimErrPathPrefix should be used instead when a root prefix is known
func RestoreErrPath(err error, path string) error {
	if pe, ok := err.(*os.PathError); ok {
		pe.Path = path
	} else if err != nil {
		slog.Warn("RestorePathErr: unhandled type", "type", fmt.Sprintf("%T", err))
	}
	return err
}

// TrimErrPathPrefix will trim a prefix from the path of an error, which is useful for both removing buildkit mount root paths and referencing uncleaned paths
func TrimErrPathPrefix(err error, prefix string) error {
	switch e := err.(type) {
	case *os.PathError:
		e.Path = strings.TrimPrefix(e.Path, prefix)
	case *os.LinkError:
		e.Old = strings.TrimPrefix(e.Old, prefix)
		e.New = strings.TrimPrefix(e.New, prefix)
	case nil:
	default:
		slog.Debug("TrimErrPathPrefix: unhandled type", "type", fmt.Sprintf("%T", err))
	}
	return err
}

func getRefOrEvaluate[T fileOrDirectory](ctx context.Context, t T) (bkcache.ImmutableRef, error) {
	return t.getSnapshot(ctx)
}

func asArrayInput[T any, I dagql.Input](ts []T, conv func(T) I) dagql.ArrayInput[I] {
	ins := make(dagql.ArrayInput[I], len(ts))
	for i, v := range ts {
		ins[i] = conv(v)
	}
	return ins
}

func pathResolverForMount(
	m *mount.Mount,
	mntedPath string, // if set, paths will be assumed to be provided as seen from under mntedPath
) (fscopy.PathResolver, error) {
	if m == nil {
		return nil, nil
	}
	switch m.Type {
	case "bind", "rbind":
		return func(p string) (string, error) {
			if mntedPath != "" {
				var err error
				p, err = filepath.Rel(mntedPath, p)
				if err != nil {
					return "", err
				}
			}
			return containerdfs.RootPath(m.Source, p)
		}, nil
	case "overlay":
		overlayDirs, err := overlay.GetOverlayLayers(*m)
		if err != nil {
			return nil, fmt.Errorf("failed to get overlay layers: %w", err)
		}
		return func(p string) (string, error) {
			if mntedPath != "" {
				var err error
				p, err = filepath.Rel(mntedPath, p)
				if err != nil {
					return "", err
				}
			}
			// overlayDirs is lower->upper, so iterate in reverse to check
			// upper layers first
			var resolvedUpperdirPath string
			for i := len(overlayDirs) - 1; i >= 0; i-- {
				layerRoot := overlayDirs[i]
				resolvedPath, err := containerdfs.RootPath(layerRoot, p)
				if err != nil {
					return "", err
				}
				if i == len(overlayDirs)-1 {
					resolvedUpperdirPath = resolvedPath
				}
				_, err = os.Lstat(resolvedPath)
				switch {
				case err == nil:
					return resolvedPath, nil
				case errors.Is(err, os.ErrNotExist):
					// try next layer
				default:
					return "", fmt.Errorf("failed to stat path %s in overlay layer: %w", resolvedPath, err)
				}
			}
			// path doesn't exist, so if it's gonna exist, it should be in the upperdir
			return resolvedUpperdirPath, nil
		}, nil
	default:
		return nil, nil
	}
}
