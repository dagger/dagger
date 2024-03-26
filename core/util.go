package core

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"path"
	"strconv"
	"strings"

	"github.com/dagger/dagger/core/reffs"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/llbsolver/provenance"
	"github.com/moby/buildkit/solver/pb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/runc/libcontainer/user"
	"github.com/pkg/errors"
)

type HasPBDefinitions interface {
	PBDefinitions(context.Context) ([]*pb.Definition, error)
}

type HasFieldValues interface {
	FieldValues(context.Context) ([]dagql.Typed, error)
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
	case dagql.Wrapper: // dagql.Instance
		return collectPBDefinitions(ctx, x.Unwrap())
	case HasFieldValues:
		fieldValues, err := x.FieldValues(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get field values: %w", err)
		}
		defs := []*pb.Definition{}
		for _, val := range fieldValues {
			fieldDefs, err := collectPBDefinitions(ctx, val)
			if err != nil {
				return nil, err
			}
			defs = append(defs, fieldDefs...)
		}
		return defs, nil
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

func referencedTypes[T dagql.Typed](ctx context.Context, value dagql.Typed, dag *dagql.Server) ([]T, error) {
	switch x := value.(type) {
	case dagql.String, dagql.Int, dagql.Boolean, dagql.Float:
		// nothing to do
		return nil, nil
	case dagql.Enumerable: // dagql.Array
		values := []T{}
		for i := 1; i < x.Len(); i++ {
			val, err := x.Nth(i)
			if err != nil {
				return nil, fmt.Errorf("failed to get nth value: %w", err)
			}
			elemValues, err := referencedTypes[T](ctx, val, dag)
			if err != nil {
				return nil, fmt.Errorf("failed to link nth value dependency blobs: %w", err)
			}
			values = append(values, elemValues...)
		}
		return values, nil
	case dagql.Derefable: // dagql.Nullable
		if inner, ok := x.Deref(); ok {
			return referencedTypes[T](ctx, inner, dag)
		} else {
			return nil, nil
		}
	case HasFieldValues:
		fieldValues, err := x.FieldValues(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get field values: %w", err)
		}
		values := []T{}
		for _, val := range fieldValues {
			fieldValues, err := referencedTypes[T](ctx, val, dag)
			if err != nil {
				return nil, err
			}
			values = append(values, fieldValues...)
		}
		return values, nil
	case dagql.IDable:
		return dagql.ReferencedTypes[T](ctx, x.ID(), dag, true)
	default:
		slog.Warn("referencedTypes: unhandled type", "type", fmt.Sprintf("%T", value))
		return nil, nil
	}
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
	if def.Def == nil {
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

func cloneSlice[T any](src []T) []T {
	dst := make([]T, len(src))
	copy(dst, src)
	return dst
}

func cloneMap[K comparable, T any](src map[K]T) map[K]T {
	if src == nil {
		return src
	}
	dst := make(map[K]T, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func parseKeyValue(env string) (string, string) {
	parts := strings.SplitN(env, "=", 2)

	v := ""
	if len(parts) > 1 {
		v = parts[1]
	}

	return parts[0], v
}

// AddEnv adds or updates an environment variable in 'env'.
func AddEnv(env []string, name, value string) []string {
	// Implementation from the dockerfile2llb project.
	gotOne := false

	for i, envVar := range env {
		k, _ := parseKeyValue(envVar)
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
		k, v := parseKeyValue(envVar)
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
		key, value := parseKeyValue(envVar)
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

	for k, v := range src {
		dst[k] = v
	}

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

type nopCloser struct {
	io.Writer
}

func (nopCloser) Close() error {
	return nil
}

func resolveProvenance(ctx context.Context, bk *buildkit.Client, st llb.State) (*provenance.Capture, error) {
	def, err := st.Marshal(ctx)
	if err != nil {
		return nil, err
	}
	res, err := bk.Solve(ctx, bkgw.SolveRequest{
		Evaluate:   true,
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, err
	}
	p := res.Ref.Provenance()
	if p == nil {
		return nil, errors.Errorf("no provenance was resolved")
	}
	return p, nil
}

func ptr[T any](v T) *T {
	return &v
}
