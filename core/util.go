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
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/runc/libcontainer/user"
)

// encodeID JSON marshals and base64-encodes an arbitrary payload.
func encodeID(payload any) (string, error) {
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	b64Bytes := make([]byte, base64.StdEncoding.EncodedLen(len(jsonBytes)))
	base64.StdEncoding.Encode(b64Bytes, jsonBytes)

	return string(b64Bytes), nil
}

// decodeID base64-decodes and JSON unmarshals an ID into an arbitrary payload.
func decodeID[T ~string](payload any, id T) error {
	jsonBytes := make([]byte, base64.StdEncoding.DecodedLen(len(id)))
	n, err := base64.StdEncoding.Decode(jsonBytes, []byte(id))
	if err != nil {
		return fmt.Errorf("failed to decode %T bytes: %v: %v", payload, err, payload)
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

func resolveUIDGID(ctx context.Context, fsSt llb.State, gw bkgw.Client, platform specs.Platform, owner string) (int, int, error) {
	if owner == "" {
		// default to root
		return 0, 0, nil
	}

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
			return -1, -1, fmt.Errorf("open fs state for name->id: %w", err)
		}
	}

	if uname != "" {
		uid, err = findUID(fs, uname)
		if err != nil {
			return -1, -1, fmt.Errorf("find uid: %w", err)
		}
	}

	if gname != "" {
		gid, err = findGID(fs, gname)
		if err != nil {
			return -1, -1, fmt.Errorf("find gid: %w", err)
		}
	}

	if !hasGroup {
		gid = uid
	}

	return uid, gid, nil
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
