package cacerts

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/opencontainers/runtime-spec/specs-go"
)

func newContainerFS(spec *specs.Spec, executeContainer executeContainerFunc) (*containerFS, error) {
	// hopefully the source is never a symlink, but resolve them just in case
	// in order to simplify later code
	for i, m := range spec.Mounts {
		switch m.Type {
		case "proc", "sysfs", "tmpfs", "devpts", "shm", "mqueue", "cgroup", "cgroup2":
			continue
		default:
		}
		var err error
		if m.Source, err = filepath.EvalSymlinks(m.Source); err != nil {
			return nil, err
		}
		spec.Mounts[i] = m
	}

	ctrFS := &containerFS{spec: spec, executeContainer: executeContainer}
	ctrFS.mounts = []mount{{
		Mount: specs.Mount{
			Destination: "/",
			Source:      spec.Root.Path,
		},
		ResolvedDest: "/",
	}}

	for _, m := range spec.Mounts {
		resolvedDest, err := ctrFS.mountPointPath(m.Destination)
		if err != nil {
			return nil, err
		}
		ctrFS.mounts = append(ctrFS.mounts, mount{
			Mount:        m,
			ResolvedDest: resolvedDest,
		})
	}
	return ctrFS, nil
}

type containerFS struct {
	spec             *specs.Spec
	executeContainer executeContainerFunc
	mounts           []mount
}

type mount struct {
	specs.Mount
	ResolvedDest string
}

func (ctrFS *containerFS) Open(name string) (fs.File, error) {
	hostPath, err := ctrFS.hostPath(name)
	if err != nil {
		return nil, err
	}
	return os.Open(hostPath)
}

func (ctrFS *containerFS) OpenFile(name string, flag int, perm fs.FileMode) (*os.File, error) {
	hostPath, err := ctrFS.hostPath(name)
	if err != nil {
		return nil, err
	}
	return os.OpenFile(hostPath, flag, perm)
}

func (ctrFS *containerFS) ReadDir(name string) ([]fs.DirEntry, error) {
	hostPath, err := ctrFS.hostPath(name)
	if err != nil {
		return nil, err
	}
	return os.ReadDir(hostPath)
}

func (ctrFS *containerFS) Stat(name string) (fs.FileInfo, error) {
	hostPath, err := ctrFS.hostPath(name)
	if err != nil {
		return nil, err
	}
	return os.Stat(hostPath)
}

func (ctrFS *containerFS) Lstat(name string) (fs.FileInfo, error) {
	hostPath, err := ctrFS.hostPath(name)
	if err != nil {
		return nil, err
	}
	return os.Lstat(hostPath)
}

func (ctrFS *containerFS) Symlink(oldname, newname string) error {
	newHostPath, err := ctrFS.hostPath(newname)
	if err != nil {
		return err
	}
	return os.Symlink(oldname, newHostPath)
}

func (ctrFS *containerFS) Readlink(name string) (string, error) {
	hostPath, err := ctrFS.hostPath(name)
	if err != nil {
		return "", err
	}
	return os.Readlink(hostPath)
}

func (ctrFS *containerFS) ReadFile(path string) ([]byte, error) {
	hostPath, err := ctrFS.hostPath(path)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(hostPath)
}

func (ctrFS *containerFS) WriteFile(path string, data []byte, perm fs.FileMode) error {
	hostPath, err := ctrFS.hostPath(path)
	if err != nil {
		return err
	}
	return os.WriteFile(hostPath, data, perm)
}

func (ctrFS *containerFS) Remove(path string) error {
	hostPath, err := ctrFS.hostPath(path)
	if err != nil {
		return err
	}
	return os.Remove(hostPath)
}

func (ctrFS *containerFS) MkdirAll(path string, perm fs.FileMode) error {
	hostPath, err := ctrFS.hostPath(path)
	if err != nil {
		return err
	}
	return os.MkdirAll(hostPath, perm)
}

func (ctrFS *containerFS) LookPath(cmd string) (string, error) {
	if filepath.IsAbs(cmd) {
		return cmd, nil
	}

	// TODO: may caller need to augment PATH with sbins when user not root
	var pathEnvVal string
	for _, env := range ctrFS.spec.Process.Env {
		if strings.HasPrefix(env, "PATH=") {
			pathEnvVal = strings.TrimPrefix(env, "PATH=")
			break
		}
	}
	if pathEnvVal == "" {
		pathEnvVal = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}
	for _, dir := range filepath.SplitList(pathEnvVal) {
		execPath := filepath.Join(dir, cmd)
		stat, err := ctrFS.Stat(execPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", err
		}
		if stat.Mode().IsRegular() && stat.Mode().Perm()&0111 != 0 {
			return execPath, nil
		}
	}
	return "", exec.ErrNotFound
}

func (ctrFS *containerFS) Exec(ctx context.Context, args ...string) error {
	return ctrFS.executeContainer(ctx, args...)
}

func (ctrFS *containerFS) AnyPathExists(paths []string) (bool, error) {
	// TODO: parallelize?
	for _, path := range paths {
		exists, err := ctrFS.PathExists(path)
		if err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
	}
	return false, nil
}

func (ctrFS *containerFS) PathExists(path string) (bool, error) {
	_, err := ctrFS.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (ctrFS *containerFS) OSReleaseFileContains(ids [][]byte, idLikes [][]byte) (bool, error) {
	// read /etc/os-release line by line
	f, err := ctrFS.Open("/etc/os-release")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)

	idDone := ids == nil
	idLikeDone := idLikes == nil
	for scanner.Scan() {
		if idDone && idLikeDone {
			break
		}
		line := scanner.Bytes()
		switch {
		case len(line) > 0 && line[0] == '#':
			// skip comment
		case !idDone && bytes.HasPrefix(line, []byte("ID=")):
			idDone = true
			val := bytes.TrimPrefix(line, []byte("ID="))
			val = bytes.Trim(bytes.TrimSpace(val), `"`)
			for _, id := range ids {
				if bytes.Equal(val, id) {
					return true, nil
				}
			}
		case !idLikeDone && bytes.HasPrefix(line, []byte("ID_LIKE=")):
			idLikeDone = true
			val := bytes.TrimPrefix(line, []byte("ID_LIKE="))
			val = bytes.Trim(bytes.TrimSpace(val), `"`)
			for _, v := range bytes.Fields(val) {
				for _, idLike := range idLikes {
					if bytes.Equal(v, idLike) {
						return true, nil
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func (ctrFS *containerFS) ReadCABundleFile(path string) (map[string]struct{}, error) {
	certs := make(map[string]struct{})
	f, err := ctrFS.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var curCert []byte
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			if len(curCert) > 0 {
				certs[string(curCert[:len(curCert)-1])] = struct{}{}
				curCert = nil
			}
			continue
		}
		curCert = append(curCert, line...)
		curCert = append(curCert, byte('\n'))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(curCert) > 0 {
		certs[string(curCert[:len(curCert)-1])] = struct{}{}
	}
	return certs, nil
}

func (ctrFS *containerFS) RemoveCertsFromCABundle(path string, certs map[string]string) error {
	f, err := ctrFS.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var updatedFileContents []byte
	var curCert []byte
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			if len(curCert) > 0 {
				if _, exists := certs[string(curCert[:len(curCert)-1])]; !exists {
					updatedFileContents = append(updatedFileContents, curCert...)
					updatedFileContents = append(updatedFileContents, byte('\n'))
				}
				curCert = nil
			}
			continue
		}
		curCert = append(curCert, line...)
		curCert = append(curCert, byte('\n'))
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if len(curCert) > 0 {
		if _, exists := certs[string(curCert[:len(curCert)-1])]; !exists {
			updatedFileContents = append(updatedFileContents, curCert...)
			updatedFileContents = append(updatedFileContents, byte('\n'))
		}
	}

	f.Close()
	// TODO: preserve permissions/ownership/mtime
	if err := ctrFS.WriteFile(path, updatedFileContents, 0644); err != nil {
		return err
	}
	return nil
}

func (ctrFS *containerFS) ReadCustomCADir(path string) (
	certsToFileName map[string]string,
	symlinks map[string]string,
	rerr error,
) {
	hostPath, err := ctrFS.hostPath(path)
	if err != nil {
		return nil, nil, err
	}
	return ReadHostCustomCADir(hostPath)
}

func (ctrFS *containerFS) DirIsEmpty(path string) (bool, error) {
	dirEnts, err := ctrFS.ReadDir(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, err
	}
	return len(dirEnts) == 0, nil
}

func (ctrFS *containerFS) mountPointPath(containerPath string) (string, error) {
	containerPath, _, err := ctrFS.resolvePath(containerPath, true, 0)
	return containerPath, err
}

func (ctrFS *containerFS) hostPath(containerPath string) (string, error) {
	_, hostPath, err := ctrFS.resolvePath(containerPath, false, 0)
	if hostPath == "" {
		return "", fmt.Errorf("cannot resolve path %q", containerPath)
	}
	return hostPath, err
}

func (ctrFS *containerFS) resolvePath(path string, resolveBase bool, linkCount int) (
	containerPath string,
	hostPath string,
	rerr error,
) {
	if linkCount > 255 {
		return "", "", errors.New("too many links")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join("/", path)
	}

	split := strings.Split(path, "/")
	curPath := "/" // invariant: curPath never contains symlinks at beginning of each loop
	var srcPath string
	for i, part := range split {
		if part == "" {
			continue
		}
		curPath = filepath.Join(curPath, part)
		rest := strings.Join(split[i+1:], "/")

		// figure out the mount point that must contain curPath
		var resolvedMount *mount
		var relPath string
		for j := len(ctrFS.mounts) - 1; j >= 0; j-- {
			resolvedMount = &ctrFS.mounts[j]
			var err error
			relPath, err = filepath.Rel(resolvedMount.ResolvedDest, curPath)
			if err != nil {
				return "", "", err
			}
			if filepath.IsLocal(relPath) {
				break
			}
		}

		switch resolvedMount.Type {
		case "proc", "sysfs", "tmpfs", "devpts", "shm", "mqueue", "cgroup", "cgroup2":
			// can't resolve any paths under special mounts
			return filepath.Join(curPath, rest), "", nil
		default:
		}

		srcPath = filepath.Join(resolvedMount.Source, relPath)
		if !resolveBase && rest == "" {
			// we are on the last part of the path, don't follow any symlinks
			break
		}

		stat, err := os.Lstat(srcPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// cannot be any symlinks to resolve anymore
				return filepath.Join(curPath, rest), filepath.Join(srcPath, rest), nil
			}
			return "", "", err
		}
		if stat.Mode().Type() == fs.ModeSymlink {
			target, err := os.Readlink(srcPath)
			if err != nil {
				return "", "", err
			}
			if filepath.IsAbs(target) {
				return ctrFS.resolvePath(filepath.Join(target, rest), resolveBase, linkCount+1)
			}
			return ctrFS.resolvePath(filepath.Join(filepath.Dir(curPath), target, rest), resolveBase, linkCount+1)
		}
	}
	return filepath.Clean(curPath), filepath.Clean(srcPath), nil
}

func ReadHostCustomCADir(path string) (
	certsToFileName map[string]string,
	symlinks map[string]string,
	rerr error,
) {
	certsToFileName = make(map[string]string)
	symlinks = make(map[string]string)

	dirEnts, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return certsToFileName, symlinks, nil
		}
		return nil, nil, err
	}
	for _, dirEnt := range dirEnts {
		dirEntPath := filepath.Join(path, dirEnt.Name())
		switch dirEnt.Type() {
		case os.ModeSymlink:
			linkPath, err := os.Readlink(dirEntPath)
			if err != nil {
				return nil, nil, err
			}
			symlinks[dirEnt.Name()] = linkPath
		case os.ModeDir:
			// TODO: handle?
		default:
			// TODO: only read .pem/.crt files?
			bs, err := os.ReadFile(dirEntPath)
			if err != nil {
				return nil, nil, err
			}
			certsToFileName[strings.TrimSpace(string(bs))] = dirEnt.Name()
		}
	}
	return certsToFileName, symlinks, nil
}
