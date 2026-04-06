package llbtodagger

import (
	"fmt"
	"strings"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
)

func (c *converter) convertExec(exec *buildkit.ExecOp) (*call.ID, error) {
	if exec == nil || exec.ExecOp == nil {
		return nil, unsupported(opDigest(exec.OpDAG), "exec", "missing exec op")
	}

	if exec.Network != pb.NetMode_UNSET && exec.Network != pb.NetMode_NONE && exec.Network != pb.NetMode_HOST {
		return nil, unsupported(opDigest(exec.OpDAG), "exec", fmt.Sprintf("unsupported network mode %v", exec.Network))
	}
	if exec.Security != pb.SecurityMode_SANDBOX && exec.Security != pb.SecurityMode_INSECURE {
		return nil, unsupported(opDigest(exec.OpDAG), "exec", fmt.Sprintf("unsupported security mode %v", exec.Security))
	}
	if exec.Meta == nil {
		return nil, unsupported(opDigest(exec.OpDAG), "exec", "missing exec meta")
	}
	if exec.Meta.Hostname != "" {
		return nil, unsupported(opDigest(exec.OpDAG), "exec", "hostname override is unsupported")
	}
	if len(exec.Meta.ExtraHosts) > 0 {
		return nil, unsupported(opDigest(exec.OpDAG), "exec", "extra hosts are unsupported")
	}
	if len(exec.Meta.Ulimit) > 0 {
		return nil, unsupported(opDigest(exec.OpDAG), "exec", "ulimit is unsupported")
	}
	if exec.Meta.CgroupParent != "" {
		return nil, unsupported(opDigest(exec.OpDAG), "exec", "cgroup parent is unsupported")
	}
	if len(exec.Meta.ValidExitCodes) > 0 {
		return nil, unsupported(opDigest(exec.OpDAG), "exec", "valid exit code overrides are unsupported")
	}

	inputIDs := make([]*call.ID, len(exec.Inputs))
	for i, in := range exec.Inputs {
		id, err := c.convertOp(in)
		if err != nil {
			return nil, err
		}
		inputIDs[i] = id
	}

	rootMount, err := findRootExecMount(exec.Mounts)
	if err != nil {
		return nil, unsupported(opDigest(exec.OpDAG), "exec", err.Error())
	}

	rootDirID, err := c.resolveExecMountInputDir(opDigest(exec.OpDAG), rootMount, inputIDs)
	if err != nil {
		return nil, err
	}

	var ctrID *call.ID
	if rootMount.Input != pb.Empty {
		rootInputIdx := int(rootMount.Input)
		if rootInputIdx >= 0 && rootInputIdx < len(inputIDs) {
			rootInputID := inputIDs[rootInputIdx]
			if rootInputID != nil && rootInputID.Type().NamedType() == containerType().NamedType {
				if cleanPath(rootMount.Selector) == "/" {
					ctrID = rootInputID
				} else {
					ctrID = appendCall(rootInputID, containerType(), "withRootfs", argID("directory", rootDirID))
				}
			}
		}
	}
	if ctrID == nil {
		ctrID, err = queryContainerID(exec.Platform)
		if err != nil {
			return nil, fmt.Errorf("llbtodagger: exec %s: %w", opDigest(exec.OpDAG), err)
		}
		ctrID = appendCall(ctrID, containerType(), "withRootfs", argID("directory", rootDirID))
	}

	addedMountPaths := make([]string, 0, len(exec.Mounts))
	addedMountPathSet := map[string]struct{}{}
	addedUnixSocketPaths := make([]string, 0, len(exec.Mounts))
	addedUnixSocketPathSet := map[string]struct{}{}
	addedSecretEnvNames := make([]string, 0, len(exec.Secretenv))
	addedSecretEnvSet := map[string]struct{}{}

	for _, secretEnv := range exec.Secretenv {
		secretID, include, err := c.resolveSecretID(opDigest(exec.OpDAG), secretEnv.ID, secretEnv.Optional)
		if err != nil {
			return nil, err
		}
		if !include {
			continue
		}
		ctrID = appendCall(
			ctrID,
			containerType(),
			"withSecretVariable",
			argString("name", secretEnv.Name),
			argID("secret", secretID),
		)
		if _, exists := addedSecretEnvSet[secretEnv.Name]; !exists {
			addedSecretEnvSet[secretEnv.Name] = struct{}{}
			addedSecretEnvNames = append(addedSecretEnvNames, secretEnv.Name)
		}
	}

	for _, m := range exec.Mounts {
		if m == rootMount {
			continue
		}
		if m.ResultID != "" {
			return nil, unsupported(opDigest(exec.OpDAG), "exec", "mount resultID is unsupported")
		}
		if m.ContentCache != pb.MountContentCache_DEFAULT {
			return nil, unsupported(opDigest(exec.OpDAG), "exec", "non-default mount content cache is unsupported")
		}

		switch m.MountType {
		case pb.MountType_BIND:
			dirID, err := c.resolveExecMountInputDir(opDigest(exec.OpDAG), m, inputIDs)
			if err != nil {
				return nil, err
			}
			args := []*call.Argument{
				argString("path", m.Dest),
				argID("source", dirID),
			}
			if m.Readonly {
				args = append(args, argBool("readOnly", true))
			}
			ctrID = appendCall(
				ctrID,
				containerType(),
				"withMountedDirectory",
				args...,
			)
			path := cleanPath(m.Dest)
			if _, exists := addedMountPathSet[path]; !exists {
				addedMountPathSet[path] = struct{}{}
				addedMountPaths = append(addedMountPaths, path)
			}

		case pb.MountType_CACHE:
			if m.CacheOpt == nil || m.CacheOpt.ID == "" {
				return nil, unsupported(opDigest(exec.OpDAG), "exec", "cache mount is missing cache ID")
			}
			sharing, err := mountSharingEnum(m.CacheOpt.Sharing)
			if err != nil {
				return nil, unsupported(opDigest(exec.OpDAG), "exec", err.Error())
			}
			cacheID := appendCall(call.New(), cacheVolumeType(), "cacheVolume", argString("key", m.CacheOpt.ID))
			args := []*call.Argument{
				argString("path", m.Dest),
				argID("cache", cacheID),
				argEnum("sharing", sharing),
			}
			if m.Input != pb.Empty {
				sourceDirID, err := c.resolveExecMountInputDir(opDigest(exec.OpDAG), m, inputIDs)
				if err != nil {
					return nil, err
				}
				args = append(args, argID("source", sourceDirID))
			}
			ctrID = appendCall(ctrID, containerType(), "withMountedCache", args...)
			path := cleanPath(m.Dest)
			if _, exists := addedMountPathSet[path]; !exists {
				addedMountPathSet[path] = struct{}{}
				addedMountPaths = append(addedMountPaths, path)
			}

		case pb.MountType_TMPFS:
			args := []*call.Argument{argString("path", m.Dest)}
			if m.TmpfsOpt != nil && m.TmpfsOpt.Size_ > 0 {
				args = append(args, argInt("size", m.TmpfsOpt.Size_))
			}
			ctrID = appendCall(ctrID, containerType(), "withMountedTemp", args...)
			path := cleanPath(m.Dest)
			if _, exists := addedMountPathSet[path]; !exists {
				addedMountPathSet[path] = struct{}{}
				addedMountPaths = append(addedMountPaths, path)
			}

		case pb.MountType_SECRET:
			if m.SecretOpt == nil {
				return nil, unsupported(opDigest(exec.OpDAG), "exec", "secret mount is missing secret options")
			}
			secretID, include, err := c.resolveSecretID(opDigest(exec.OpDAG), m.SecretOpt.ID, m.SecretOpt.Optional)
			if err != nil {
				return nil, err
			}
			if !include {
				continue
			}
			ctrID = appendCall(
				ctrID,
				containerType(),
				"withMountedSecret",
				argString("path", m.Dest),
				argID("source", secretID),
				argString("owner", fmt.Sprintf("%d:%d", m.SecretOpt.Uid, m.SecretOpt.Gid)),
				argInt("mode", int64(m.SecretOpt.Mode)),
			)
			path := cleanPath(m.Dest)
			if _, exists := addedMountPathSet[path]; !exists {
				addedMountPathSet[path] = struct{}{}
				addedMountPaths = append(addedMountPaths, path)
			}
		case pb.MountType_SSH:
			if m.SSHOpt == nil {
				return nil, unsupported(opDigest(exec.OpDAG), "exec", "ssh mount is missing ssh options")
			}
			if m.SSHOpt.Mode != 0 && m.SSHOpt.Mode != 0o600 {
				return nil, unsupported(opDigest(exec.OpDAG), "exec", fmt.Sprintf("ssh mount mode %04o is unsupported", m.SSHOpt.Mode))
			}
			socketID, include, err := c.resolveSSHSocketID(opDigest(exec.OpDAG), m.SSHOpt.ID, m.SSHOpt.Optional)
			if err != nil {
				return nil, err
			}
			if !include {
				continue
			}
			ctrID = appendCall(
				ctrID,
				containerType(),
				"withUnixSocket",
				argString("path", m.Dest),
				argID("source", socketID),
				argString("owner", fmt.Sprintf("%d:%d", m.SSHOpt.Uid, m.SSHOpt.Gid)),
			)
			path := cleanPath(m.Dest)
			if _, exists := addedUnixSocketPathSet[path]; !exists {
				addedUnixSocketPathSet[path] = struct{}{}
				addedUnixSocketPaths = append(addedUnixSocketPaths, path)
			}
		default:
			return nil, unsupported(opDigest(exec.OpDAG), "exec", fmt.Sprintf("unsupported mount type %v", m.MountType))
		}
	}

	for _, envKV := range exec.Meta.Env {
		name, val, ok := strings.Cut(envKV, "=")
		if !ok {
			return nil, unsupported(opDigest(exec.OpDAG), "exec", fmt.Sprintf("invalid env entry %q", envKV))
		}
		ctrID = appendCall(
			ctrID,
			containerType(),
			"withEnvVariable",
			argString("name", name),
			argString("value", val),
		)
	}

	if exec.Meta.User != "" {
		ctrID = appendCall(ctrID, containerType(), "withUser", argString("name", exec.Meta.User))
	}
	if exec.Meta.Cwd != "" {
		ctrID = appendCall(ctrID, containerType(), "withWorkdir", argString("path", exec.Meta.Cwd))
	}

	withExecArgs := []*call.Argument{
		argStringList("args", exec.Meta.Args),
	}
	if exec.Network == pb.NetMode_NONE {
		withExecArgs = append(withExecArgs, argBool("noNetwork", true))
	}
	if exec.Network == pb.NetMode_HOST {
		withExecArgs = append(withExecArgs, argBool("hostNetwork", true))
	}
	if c.noInit {
		withExecArgs = append(withExecArgs, argBool("noInit", true))
	}
	if exec.Security == pb.SecurityMode_INSECURE {
		withExecArgs = append(withExecArgs, argBool("insecureRootCapabilities", true))
	}
	withExecID := appendCall(ctrID, containerType(), "withExec", withExecArgs...)

	outMount, ok := findMountByOutput(exec.Mounts, exec.OutputIndex())
	if !ok {
		return nil, unsupported(opDigest(exec.OpDAG), "exec", fmt.Sprintf("no mount for output index %d", exec.OutputIndex()))
	}
	if outMount.Dest == "/" {
		cleanedID := withExecID
		for _, path := range addedMountPaths {
			if path == "/" {
				continue
			}
			cleanedID = appendCall(cleanedID, containerType(), "withoutMount", argString("path", path))
		}
		for _, path := range addedUnixSocketPaths {
			if path == "/" {
				continue
			}
			cleanedID = appendCall(cleanedID, containerType(), "withoutUnixSocket", argString("path", path))
		}
		for _, name := range addedSecretEnvNames {
			cleanedID = appendCall(cleanedID, containerType(), "withoutSecretVariable", argString("name", name))
		}
		return cleanedID, nil
	}
	return appendCall(withExecID, directoryType(), "directory", argString("path", outMount.Dest)), nil
}

func (c *converter) resolveSecretID(opDgst digest.Digest, llbSecretID string, optional bool) (*call.ID, bool, error) {
	if llbSecretID == "" {
		if optional {
			return nil, false, nil
		}
		return nil, false, unsupported(opDgst, "exec", "secret id is empty")
	}

	secretID, ok := c.secretIDsByLLBID[llbSecretID]
	if ok && secretID != nil {
		if secretID.Type().NamedType() != secretType().NamedType {
			return nil, false, unsupported(opDgst, "exec", fmt.Sprintf("mapped secret %q has non-Secret type %q", llbSecretID, secretID.Type().NamedType()))
		}
		return secretID, true, nil
	}

	if optional {
		return nil, false, nil
	}

	if len(c.secretIDsByLLBID) == 0 {
		return nil, false, unsupported(opDgst, "exec", fmt.Sprintf("secret %q is required but no secret mappings were provided", llbSecretID))
	}
	return nil, false, unsupported(opDgst, "exec", fmt.Sprintf("secret %q is required but was not provided", llbSecretID))
}

func (c *converter) resolveSSHSocketID(opDgst digest.Digest, llbSSHID string, optional bool) (*call.ID, bool, error) {
	socketID, ok := c.sshSocketIDsByLLBID[llbSSHID]
	if (!ok || socketID == nil) && llbSSHID != "" {
		// Dockerfile-based callers with a single ssh socket can provide a
		// default mapping under the empty key to satisfy any ssh ID.
		socketID, ok = c.sshSocketIDsByLLBID[""]
	}
	if ok && socketID != nil {
		if socketID.Type().NamedType() != socketType().NamedType {
			return nil, false, unsupported(opDgst, "exec", fmt.Sprintf("mapped ssh socket %q has non-Socket type %q", llbSSHID, socketID.Type().NamedType()))
		}
		return socketID, true, nil
	}

	if optional {
		return nil, false, nil
	}

	if len(c.sshSocketIDsByLLBID) == 0 {
		return nil, false, unsupported(opDgst, "exec", fmt.Sprintf("ssh mount %q is required but no ssh socket mappings were provided", llbSSHID))
	}
	return nil, false, unsupported(opDgst, "exec", fmt.Sprintf("ssh mount %q is required but was not provided", llbSSHID))
}

func findRootExecMount(mounts []*pb.Mount) (*pb.Mount, error) {
	for _, m := range mounts {
		if m != nil && m.Dest == "/" && m.MountType == pb.MountType_BIND {
			return m, nil
		}
	}
	return nil, fmt.Errorf("root bind mount not found")
}

func findMountByOutput(mounts []*pb.Mount, out pb.OutputIndex) (*pb.Mount, bool) {
	for _, m := range mounts {
		if m != nil && m.Output == out {
			return m, true
		}
	}
	return nil, false
}

func (c *converter) resolveExecMountInputDir(opDgst digest.Digest, mount *pb.Mount, inputIDs []*call.ID) (*call.ID, error) {
	var dirID *call.ID
	if mount.Input == pb.Empty {
		dirID = scratchDirectoryID()
	} else {
		idx := int(mount.Input)
		if idx < 0 || idx >= len(inputIDs) {
			return nil, unsupported(opDgst, "exec", fmt.Sprintf("mount input index %d out of range", mount.Input))
		}
		var err error
		dirID, err = asDirectoryID(opDgst, "exec", inputIDs[idx])
		if err != nil {
			return nil, err
		}
	}

	selector := cleanPath(mount.Selector)
	if selector == "/" {
		return dirID, nil
	}
	return appendCall(dirID, directoryType(), "directory", argString("path", selector)), nil
}
