package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/dagger/dagger/core/reffs"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/runc/libcontainer/user"
)

// encodeID JSON marshals and base64-encodes an arbitrary payload.
func encodeID[T ~string](payload any) (T, error) {
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	b64Bytes := make([]byte, base64.StdEncoding.EncodedLen(len(jsonBytes)))
	base64.StdEncoding.Encode(b64Bytes, jsonBytes)

	return T(b64Bytes), nil
}

// decodeID base64-decodes and JSON unmarshals an ID into an arbitrary payload.
func decodeID[T ~string](payload any, id T) error {
	jsonBytes := make([]byte, base64.StdEncoding.DecodedLen(len(id)))
	n, err := base64.StdEncoding.Decode(jsonBytes, []byte(id))
	if err != nil {
		return fmt.Errorf("failed to decode %T bytes: %v: %w", payload, id, err)
	}

	jsonBytes = jsonBytes[:n]

	return json.Unmarshal(jsonBytes, payload)
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

// mirrorCh mirrors messages from one channel to another, protecting the
// destination channel from being closed.
//
// this is used to reflect Build/Solve progress in a longer-lived progress UI,
// since they close the channel when they're done.
func mirrorCh[T any](dest chan<- T) (chan T, *sync.WaitGroup) {
	wg := new(sync.WaitGroup)

	if dest == nil {
		return nil, wg
	}

	mirrorCh := make(chan T)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for event := range mirrorCh {
			dest <- event
		}
	}()

	return mirrorCh, wg
}

func resolveUIDGID(ctx context.Context, fsSt llb.State, gw bkgw.Client, platform specs.Platform, owner string) (*Ownership, error) {
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
		fs, err = reffs.OpenState(ctx, gw, fsSt, llb.Platform(platform))
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
func mergeMap(dst, src map[string]string) map[string]string {
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
//
// NOTE: there is an issue with merged ports for now.
// See: https://github.com/dagger/dagger/pull/5052#issuecomment-1546814114
func mergeImageConfig(dst, src specs.ImageConfig) specs.ImageConfig {
	res := src

	res.Env = mergeEnv(dst.Env, src.Env)
	res.Labels = mergeMap(dst.Labels, src.Labels)

	return res
}
