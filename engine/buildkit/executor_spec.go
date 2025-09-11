package buildkit

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/containerd/containerd/v2/core/mount"
	ctdoci "github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/continuity/fs"
	runc "github.com/containerd/go-runc"
	"github.com/dagger/dagger/engine/buildkit/resources"
	"github.com/dagger/dagger/engine/client/pathutil"
	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/internal/buildkit/executor"
	"github.com/dagger/dagger/internal/buildkit/executor/oci"
	randid "github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/internal/buildkit/util/bklog"
	bknetwork "github.com/dagger/dagger/internal/buildkit/util/network"
	"github.com/dagger/dagger/util/cleanups"
	"github.com/docker/docker/pkg/idtools"
	"github.com/google/uuid"
	"github.com/moby/sys/user"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sourcegraph/conc/pool"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
	"golang.org/x/sys/unix"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit/cacerts"
	"github.com/dagger/dagger/engine/buildkit/containerfs"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/network"
)

const (
	DaggerSessionIDEnv       = "_DAGGER_SESSION_ID"
	DaggerClientIDEnv        = "_DAGGER_NESTED_CLIENT_ID"
	DaggerCallDigestEnv      = "_DAGGER_CALL_DIGEST"
	DaggerEngineVersionEnv   = "_DAGGER_ENGINE_VERSION"
	DaggerRedirectStdinEnv   = "_DAGGER_REDIRECT_STDIN"
	DaggerRedirectStdoutEnv  = "_DAGGER_REDIRECT_STDOUT"
	DaggerRedirectStderrEnv  = "_DAGGER_REDIRECT_STDERR"
	DaggerHostnameAliasesEnv = "_DAGGER_HOSTNAME_ALIASES"
	DaggerNoInitEnv          = "_DAGGER_NOINIT"

	DaggerSessionPortEnv  = "DAGGER_SESSION_PORT"
	DaggerSessionTokenEnv = "DAGGER_SESSION_TOKEN"

	// this is set by buildkit, we cannot change
	BuildkitSessionIDHeader = "x-docker-expose-session-uuid"

	BuildkitQemuEmulatorMountPoint = "/dev/.buildkit_qemu_emulator"

	cgroupSampleInterval     = 3 * time.Second
	finalCgroupSampleTimeout = 3 * time.Second

	defaultHostname = "dagger"
)

var removeEnvs = map[string]struct{}{
	// envs that are used to scope cache but not needed at runtime
	DaggerCallDigestEnv:      {},
	DaggerEngineVersionEnv:   {},
	DaggerRedirectStdinEnv:   {},
	DaggerRedirectStdoutEnv:  {},
	DaggerRedirectStderrEnv:  {},
	DaggerHostnameAliasesEnv: {},
	DaggerNoInitEnv:          {},
}

type execState struct {
	id        string
	procInfo  *executor.ProcessInfo
	rootMount executor.Mount
	mounts    []executor.Mount

	cleanups *cleanups.Cleanups

	spec             *specs.Spec
	networkNamespace bknetwork.Namespace
	rootfsPath       string
	uid              uint32
	gid              uint32
	sgids            []uint32
	resolvConfPath   string
	hostsFilePath    string
	exitCodePath     string
	metaMount        *specs.Mount
	origEnvMap       map[string]string

	startedOnce *sync.Once
	startedCh   chan<- struct{}

	doneErr error
	done    chan struct{}
}

func newExecState(
	id string,
	procInfo *executor.ProcessInfo,
	rootMount executor.Mount,
	mounts []executor.Mount,
	startedCh chan<- struct{},
) *execState {
	return &execState{
		id:          id,
		procInfo:    procInfo,
		rootMount:   rootMount,
		mounts:      mounts,
		cleanups:    &cleanups.Cleanups{},
		startedOnce: &sync.Once{},
		startedCh:   startedCh,
		done:        make(chan struct{}),
	}
}

type executorSetupFunc func(context.Context, *execState) error

//nolint:gocyclo
func (w *Worker) setupNetwork(ctx context.Context, state *execState) error {
	provider, ok := w.networkProviders[state.procInfo.Meta.NetMode]
	if !ok {
		return fmt.Errorf("unknown network mode %s", state.procInfo.Meta.NetMode)
	}
	// if our process spec allows changes to the netns and doesn't have a hostname, assign one so buildkit doesn't default to pooled network namespaces
	// ideally, we'd be less aggressive and find a way to CLONE_NEWNET the parent netns, but for now isolation is preferable to unpredictable rule bleeding.
	if state.procInfo.Meta.SecurityMode == pb.SecurityMode_INSECURE && state.procInfo.Meta.Hostname == "" {
		state.procInfo.Meta.Hostname = uuid.NewString()
	}
	if state.procInfo.Meta.Hostname == "" {
		state.procInfo.Meta.Hostname = defaultHostname
	}
	networkNamespace, err := provider.New(ctx, state.procInfo.Meta.Hostname)
	if err != nil {
		return fmt.Errorf("create network namespace: %w", err)
	}
	state.cleanups.Add("close network namespace", networkNamespace.Close)
	state.networkNamespace = networkNamespace

	state.resolvConfPath, err = oci.GetResolvConf(ctx, w.executorRoot, w.idmap, w.dns, state.procInfo.Meta.NetMode)
	if err != nil {
		return fmt.Errorf("get base resolv.conf: %w", err)
	}

	var cleanupBaseHosts func()
	state.hostsFilePath, cleanupBaseHosts, err = oci.GetHostsFile(
		ctx, w.executorRoot, state.procInfo.Meta.ExtraHosts, w.idmap, state.procInfo.Meta.Hostname)
	if err != nil {
		return fmt.Errorf("get base hosts file: %w", err)
	}
	if cleanupBaseHosts != nil {
		state.cleanups.Add("cleanup base hosts file", cleanups.Infallible(cleanupBaseHosts))
	}

	if w.execMD == nil || w.execMD.SessionID == "" {
		return nil
	}

	extraSearchDomains := []string{}
	extraSearchDomains = append(extraSearchDomains, w.execMD.ExtraSearchDomains...)
	extraSearchDomains = append(extraSearchDomains, network.SessionDomain(w.execMD.SessionID))

	baseResolvFile, err := os.Open(state.resolvConfPath)
	if err != nil {
		return fmt.Errorf("open base resolv.conf: %w", err)
	}
	defer baseResolvFile.Close()

	baseResolvStat, err := baseResolvFile.Stat()
	if err != nil {
		return fmt.Errorf("stat base resolv.conf: %w", err)
	}

	ctrResolvFile, err := os.CreateTemp("", "resolv.conf")
	if err != nil {
		return fmt.Errorf("create container resolv.conf tmp file: %w", err)
	}
	defer ctrResolvFile.Close()
	state.resolvConfPath = ctrResolvFile.Name()
	state.cleanups.Add("remove resolv.conf", func() error {
		return os.RemoveAll(state.resolvConfPath)
	})

	if err := ctrResolvFile.Chmod(baseResolvStat.Mode().Perm()); err != nil {
		return fmt.Errorf("chmod resolv.conf: %w", err)
	}

	scanner := bufio.NewScanner(baseResolvFile)
	var replaced bool
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "search") {
			if _, err := fmt.Fprintln(ctrResolvFile, line); err != nil {
				return fmt.Errorf("write resolv.conf: %w", err)
			}
			continue
		}

		domains := strings.Fields(line)[1:]
		domains = append(domains, extraSearchDomains...)
		if _, err := fmt.Fprintln(ctrResolvFile, "search", strings.Join(domains, " ")); err != nil {
			return fmt.Errorf("write resolv.conf: %w", err)
		}
		replaced = true
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read resolv.conf: %w", err)
	}
	if !replaced {
		if _, err := fmt.Fprintln(ctrResolvFile, "search", strings.Join(extraSearchDomains, " ")); err != nil {
			return fmt.Errorf("write resolv.conf: %w", err)
		}
	}

	if len(w.execMD.HostAliases) == 0 {
		return nil
	}

	baseHostsFile, err := os.Open(state.hostsFilePath)
	if err != nil {
		return fmt.Errorf("open base hosts file: %w", err)
	}
	defer baseHostsFile.Close()

	baseHostsStat, err := baseHostsFile.Stat()
	if err != nil {
		return fmt.Errorf("stat base hosts file: %w", err)
	}

	ctrHostsFile, err := os.CreateTemp("", "hosts")
	if err != nil {
		return fmt.Errorf("create container hosts tmp file: %w", err)
	}
	defer ctrHostsFile.Close()
	state.hostsFilePath = ctrHostsFile.Name()
	state.cleanups.Add("remove hosts file", func() error {
		return os.RemoveAll(state.hostsFilePath)
	})

	if err := ctrHostsFile.Chmod(baseHostsStat.Mode().Perm()); err != nil {
		return fmt.Errorf("chmod hosts file: %w", err)
	}

	if _, err := io.Copy(ctrHostsFile, baseHostsFile); err != nil {
		return fmt.Errorf("copy base hosts file: %w", err)
	}

	for target, aliases := range w.execMD.HostAliases {
		var ips []net.IP
		var errs error
		for _, domain := range append([]string{""}, extraSearchDomains...) {
			qualified := target
			if domain != "" {
				qualified += "." + domain
			}

			var err error
			ips, err = net.LookupIP(qualified)
			if err == nil {
				errs = nil // ignore prior failures
				break
			}

			errs = errors.Join(errs, err)
		}
		if errs != nil {
			return fmt.Errorf("lookup %s for hosts file: %w", target, errs)
		}

		for _, ip := range ips {
			for _, alias := range aliases {
				if _, err := fmt.Fprintf(ctrHostsFile, "\n%s\t%s\n", ip, alias); err != nil {
					return fmt.Errorf("write hosts file: %w", err)
				}
			}
		}
	}

	return nil
}

func (w *Worker) setupVolumes(_ context.Context, state *execState) error {
	if w.execMD == nil {
		return nil
	}

	for _, hm := range w.execMD.HostMounts {
		absSrc, err := pathutil.Abs(hm.Source)
		if err != nil {
			return fmt.Errorf("get absolute path of host mount source: %w", err)
		}

		// ensure the source path exists
		_, err = os.Stat(absSrc)
		if err != nil {
			return fmt.Errorf("ensure host mount source exists: %w", err)
		}

		fmt.Printf("Mounting host directory %s to container at %s\n", absSrc, hm.Target)

		state.mounts = append(state.mounts, executor.Mount{
			Src:      hostBindMount{srcPath: absSrc, rw: true},
			Dest:     hm.Target,
			Readonly: false,
		})
	}
	return nil
}

type hostBindMount struct {
	srcPath string
	rw      bool
}

var _ executor.Mountable = (*hostBindMount)(nil)

func (m hostBindMount) Mount(_ context.Context, readonly bool) (executor.MountableRef, error) {
	m.rw = !readonly
	return hostBindMountRef(m), nil
}

type hostBindMountRef hostBindMount

var _ executor.MountableRef = (*hostBindMountRef)(nil)

func (m hostBindMountRef) Mount() ([]mount.Mount, func() error, error) {
	opts := []string{"rbind"}
	if !m.rw {
		opts = append(opts, "ro")
	}

	// release is a no-op: the caller that mounts this into the container
	// rootfs is responsible for unmounting the actual destination. Attempting
	// to unmount the host source here can race with the mount application and
	// may be incorrect if the source is not a mountpoint.
	return []mount.Mount{{
		Type:    "bind",
		Source:  m.srcPath,
		Options: opts,
	}}, func() error { return nil }, nil
}

func (m hostBindMountRef) IdentityMapping() *idtools.IdentityMapping {
	return nil
}

func (w *Worker) injectInit(_ context.Context, state *execState) error {
	if w.execMD != nil && w.execMD.NoInit {
		return nil
	}

	initPath := "/.init"
	state.mounts = append(state.mounts, executor.Mount{
		Src:      hostBindMount{srcPath: distconsts.DaggerInitPath},
		Dest:     initPath,
		Readonly: true,
	})
	state.procInfo.Meta.Args = append([]string{initPath}, state.procInfo.Meta.Args...)

	return nil
}

func (w *Worker) generateBaseSpec(ctx context.Context, state *execState) error {
	var extraOpts []ctdoci.SpecOpts
	if state.procInfo.Meta.ReadonlyRootFS {
		extraOpts = append(extraOpts, ctdoci.WithRootFSReadonly())
	}

	baseSpec, ociSpecCleanup, err := oci.GenerateSpec(
		ctx,
		state.procInfo.Meta,
		state.mounts,
		state.id,
		state.resolvConfPath,
		state.hostsFilePath,
		state.networkNamespace,
		w.cgroupParent,
		w.processMode,
		w.idmap,
		w.apparmorProfile,
		w.selinux,
		"",
		extraOpts...,
	)
	if err != nil {
		return err
	}
	state.cleanups.Add("base OCI spec cleanup", cleanups.Infallible(ociSpecCleanup))

	state.spec = baseSpec
	return nil
}

func (w *Worker) filterEnvs(_ context.Context, state *execState) error {
	state.origEnvMap = make(map[string]string)
	filteredEnvs := make([]string, 0, len(state.spec.Process.Env))
	for _, env := range state.spec.Process.Env {
		k, v, ok := strings.Cut(env, "=")
		if !ok {
			continue
		}
		if _, ok := removeEnvs[k]; !ok {
			state.origEnvMap[k] = v
			filteredEnvs = append(filteredEnvs, env)
		}
	}
	state.spec.Process.Env = filteredEnvs

	return nil
}

func (w *Worker) setupRootfs(ctx context.Context, state *execState) error {
	var err error
	state.rootfsPath, err = os.MkdirTemp("", "rootfs")
	if err != nil {
		return fmt.Errorf("create rootfs temp dir: %w", err)
	}
	state.cleanups.Add("remove rootfs temp dir", func() error {
		return os.RemoveAll(state.rootfsPath)
	})
	state.spec.Root.Path = state.rootfsPath

	rootMountable, err := state.rootMount.Src.Mount(ctx, false)
	if err != nil {
		return fmt.Errorf("get rootfs mountable: %w", err)
	}
	rootMnts, releaseRootMount, err := rootMountable.Mount()
	if err != nil {
		return fmt.Errorf("get rootfs mount: %w", err)
	}
	if releaseRootMount != nil {
		state.cleanups.Add("release rootfs mount", releaseRootMount)
	}
	// TODO: is this robust? the one for submounts is very complicated
	if state.rootMount.Selector != "" {
		for i, mnt := range rootMnts {
			mnt.Source, err = fs.RootPath(mnt.Source, state.rootMount.Selector)
			if err != nil {
				return fmt.Errorf("root mount %s points to invalid source: %w", state.rootMount.Selector, err)
			}
			rootMnts[i] = mnt
		}
	}
	if err := mount.All(rootMnts, state.rootfsPath); err != nil {
		return fmt.Errorf("mount rootfs: %w", err)
	}
	state.cleanups.Add("unmount rootfs", func() error {
		return mount.Unmount(state.rootfsPath, 0)
	})

	var nonRootMounts []mount.Mount
	var filteredMounts []specs.Mount
	for _, mnt := range state.spec.Mounts {
		switch {
		case mnt.Destination == MetaMountDestPath:
			state.metaMount = &mnt

		case mnt.Destination == BuildkitQemuEmulatorMountPoint:
			// buildkit puts the qemu emulator under /dev, which we aren't mounting now, so just
			// leave it be
			filteredMounts = append(filteredMounts, mnt)

		case containerfs.IsSpecialMountType(mnt.Type):
			// only keep special namespaced mounts like /proc, /sys, /dev, etc. in the actual spec
			filteredMounts = append(filteredMounts, mnt)

		default:
			// bind, overlay, etc. mounts will be done to the rootfs now rather than by runc.
			// This is to support read/write ops on them from the executor, such as filesync
			// for nested execs, stdout/err redirection, CA configuration, etc.
			nonRootMounts = append(nonRootMounts, mount.Mount{
				Type:    mnt.Type,
				Source:  mnt.Source,
				Target:  mnt.Destination,
				Options: mnt.Options,
			})
		}
	}
	state.spec.Mounts = filteredMounts

	state.cleanups.Add("cleanup rootfs stubs", cleanups.Infallible(executor.MountStubsCleaner(
		ctx,
		state.rootfsPath,
		state.mounts,
		state.procInfo.Meta.RemoveMountStubsRecursive,
	)))

	for _, mnt := range nonRootMounts {
		dstPath, err := fs.RootPath(state.rootfsPath, mnt.Target)
		if err != nil {
			return fmt.Errorf("mount %s points to invalid target: %w", mnt.Target, err)
		}

		if _, err := os.Stat(dstPath); err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("stat mount target %s: %w", dstPath, err)
			}

			// Need to check if the source is a directory or file so we can create the stub for the mount
			// with the correct type. Only bind mounts can be files (as far as we are concerned), so look
			// for that option and otherwise assume it is a directory (i.e. overlay).
			srcIsDir := true
			for _, opt := range mnt.Options {
				if opt == "bind" || opt == "rbind" {
					srcStat, err := os.Stat(mnt.Source)
					if err != nil {
						return fmt.Errorf("stat mount source %s: %w", mnt.Source, err)
					}
					srcIsDir = srcStat.IsDir()
					break
				}
			}

			if srcIsDir {
				if err := os.MkdirAll(dstPath, 0o755); err != nil {
					return fmt.Errorf("create mount target dir %s: %w", dstPath, err)
				}
			} else {
				if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
					return fmt.Errorf("create mount target parent dir %s: %w", dstPath, err)
				}
				if f, err := os.OpenFile(dstPath, os.O_CREATE, 0o755); err != nil {
					return fmt.Errorf("create mount target file %s: %w", dstPath, err)
				} else {
					f.Close()
				}
			}
		}

		if err := mnt.Mount(state.rootfsPath); err != nil {
			return fmt.Errorf("mount to rootfs %s: %w", mnt.Target, err)
		}
		state.cleanups.Add("unmount from rootfs "+mnt.Target, func() error {
			return mount.Unmount(dstPath, unix.MNT_DETACH)
		})
	}

	return nil
}

func (w *Worker) setUserGroup(_ context.Context, state *execState) error {
	var err error
	state.uid, state.gid, state.sgids, err = oci.GetUser(state.rootfsPath, state.procInfo.Meta.User)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}

	if state.spec.Process == nil {
		state.spec.Process = &specs.Process{}
	}
	state.spec.Process.User.UID = state.uid
	state.spec.Process.User.GID = state.gid
	state.spec.Process.User.AdditionalGids = state.sgids
	// ensure the primary GID is also included in the additional GID list
	if !slices.Contains(state.sgids, state.gid) {
		state.spec.Process.User.AdditionalGids = append([]uint32{state.gid}, state.sgids...)
	}

	return nil
}

func (w *Worker) setExitCodePath(_ context.Context, state *execState) error {
	if state.metaMount != nil {
		state.exitCodePath = filepath.Join(state.metaMount.Source, MetaMountExitCodePath)
	}
	return nil
}

func (w *Worker) setupStdio(_ context.Context, state *execState) error {
	if state.procInfo.Meta.Tty {
		state.spec.Process.Terminal = true
		// no more stdio setup needed
		return nil
	}
	if state.metaMount == nil {
		return nil
	}

	combinedOutputPath := filepath.Join(state.metaMount.Source, MetaMountCombinedOutputPath)
	combinedOutputFile, err := os.OpenFile(combinedOutputPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open combined output file: %w", err)
	}
	state.cleanups.Add("close container combined output file", combinedOutputFile.Close)

	var stdoutWriters []io.Writer
	if state.procInfo.Stdout != nil {
		stdoutWriters = append(stdoutWriters, state.procInfo.Stdout)
	}
	stdoutPath := filepath.Join(state.metaMount.Source, MetaMountStdoutPath)
	stdoutFile, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open stdout file: %w", err)
	}
	state.cleanups.Add("close container stdout file", stdoutFile.Close)
	stdoutWriters = append(stdoutWriters, stdoutFile)
	stdoutWriters = append(stdoutWriters, combinedOutputFile)

	var stderrWriters []io.Writer
	if state.procInfo.Stderr != nil {
		stderrWriters = append(stderrWriters, state.procInfo.Stderr)
	}
	stderrPath := filepath.Join(state.metaMount.Source, MetaMountStderrPath)
	stderrFile, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open stderr file: %w", err)
	}
	state.cleanups.Add("close container stderr file", stderrFile.Close)
	stderrWriters = append(stderrWriters, stderrFile)
	stderrWriters = append(stderrWriters, combinedOutputFile)

	if w.execMD != nil && (w.execMD.RedirectStdinPath != "" || w.execMD.RedirectStdoutPath != "" || w.execMD.RedirectStderrPath != "") {
		ctrFS, err := containerfs.NewContainerFS(state.spec, nil)
		if err != nil {
			return err
		}

		ctrCwd := state.spec.Process.Cwd
		if ctrCwd == "" {
			ctrCwd = "/"
		}
		if !path.IsAbs(ctrCwd) {
			ctrCwd = filepath.Join("/", ctrCwd)
		}

		redirectStdinPath := w.execMD.RedirectStdinPath
		if redirectStdinPath != "" {
			if state.procInfo.Stdin != nil {
				return fmt.Errorf("cannot set redirect stdin path %q when stdin is already set", redirectStdinPath)
			}
			if !path.IsAbs(redirectStdinPath) {
				redirectStdinPath = filepath.Join(ctrCwd, redirectStdinPath)
			}
			redirectStdinFile, err := ctrFS.Open(redirectStdinPath)
			if err != nil {
				return fmt.Errorf("open redirect stdin file: %w", err)
			}
			state.cleanups.Add("close redirect stdin file", redirectStdinFile.Close)
			state.procInfo.Stdin = io.NopCloser(redirectStdinFile)
		}

		redirectStdoutPath := w.execMD.RedirectStdoutPath
		if redirectStdoutPath != "" {
			if !path.IsAbs(redirectStdoutPath) {
				redirectStdoutPath = filepath.Join(ctrCwd, redirectStdoutPath)
			}
			redirectStdoutFile, err := ctrFS.OpenFile(redirectStdoutPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				return fmt.Errorf("open redirect stdout file: %w", err)
			}
			state.cleanups.Add("close redirect stdout file", redirectStdoutFile.Close)
			if err := redirectStdoutFile.Chown(int(state.spec.Process.User.UID), int(state.spec.Process.User.GID)); err != nil {
				return fmt.Errorf("chown redirect stdout file: %w", err)
			}
			stdoutWriters = append(stdoutWriters, redirectStdoutFile)
		}

		redirectStderrPath := w.execMD.RedirectStderrPath
		if redirectStderrPath != "" {
			if !path.IsAbs(redirectStderrPath) {
				redirectStderrPath = filepath.Join(ctrCwd, redirectStderrPath)
			}
			redirectStderrFile, err := ctrFS.OpenFile(redirectStderrPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				return fmt.Errorf("open redirect stderr file: %w", err)
			}
			state.cleanups.Add("close redirect stderr file", redirectStderrFile.Close)
			if err := redirectStderrFile.Chown(int(state.spec.Process.User.UID), int(state.spec.Process.User.GID)); err != nil {
				return fmt.Errorf("chown redirect stderr file: %w", err)
			}
			stderrWriters = append(stderrWriters, redirectStderrFile)
		}
	}

	state.procInfo.Stdout = nopCloser{io.MultiWriter(stdoutWriters...)}
	state.procInfo.Stderr = nopCloser{io.MultiWriter(stderrWriters...)}

	return nil
}

func (w *Worker) setupOTel(ctx context.Context, state *execState) error {
	if state.procInfo.Meta.NetMode != pb.NetMode_UNSET {
		// align with setupNetwork; otherwise we hang waiting for a netNS worker
		return nil
	}

	if w.causeCtx.IsValid() {
		ctx = trace.ContextWithSpanContext(ctx, w.causeCtx)
	}

	var destSession string
	var destClientID string
	if w.execMD != nil && w.execMD.SessionID != "" {
		destSession = w.execMD.SessionID

		// Send telemetry to the caller client, *not* the nested client (ClientID).
		//
		// If you set ClientID here, nested dagger CLI calls made against an engine running
		// as a service in Dagger will end up in a loop sending logs to themselves.
		destClientID = w.execMD.CallerClientID
	}

	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	state.cleanups.Add("close logs", stdio.Close)
	state.procInfo.Stdout = nopCloser{io.MultiWriter(stdio.Stdout, state.procInfo.Stdout)}
	state.procInfo.Stderr = nopCloser{io.MultiWriter(stdio.Stderr, state.procInfo.Stderr)}

	listener, err := runInNetNS(ctx, state, func() (net.Listener, error) {
		return net.Listen("tcp", "127.0.0.1:0")
	})
	if err != nil {
		return fmt.Errorf("internal telemetry proxy listen: %w", err)
	}
	otelSrv := &http.Server{
		Handler: http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			if r.Header == nil {
				r.Header = http.Header{}
			}
			r.Header.Set("X-Dagger-Session-ID", destSession)
			r.Header.Set("X-Dagger-Client-ID", destClientID)
			w.telemetryPubSub.ServeHTTP(rw, r)
		}),
		ReadHeaderTimeout: 5 * time.Second, // for gocritic
	}
	listenerPool := pool.New().WithErrors()
	listenerPool.Go(func() error {
		return otelSrv.Serve(listener)
	})
	state.cleanups.Add("wait for internal telemetry forwarder", func() error {
		if err := listenerPool.Wait(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})
	state.cleanups.Add("shutdown internal telemetry forwarder", cleanups.Infallible(func() {
		shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		switch err := otelSrv.Shutdown(shutdownCtx); {
		case err == nil:
			return
		case errors.Is(err, context.DeadlineExceeded):
			slog.ErrorContext(ctx, "timeout waiting for internal telemetry forwarder to shutdown", err)
		default:
			slog.ErrorContext(ctx, "failed to shutdown internal telemetry forwarder", err)
		}
	}))

	// Configure our OpenTelemetry proxy. A lot.
	otelProto := "http/protobuf"
	otelEndpoint := "http://" + listener.Addr().String()
	state.spec.Process.Env = append(state.spec.Process.Env,
		engine.OTelExporterProtocolEnv+"="+otelProto,
		engine.OTelExporterEndpointEnv+"="+otelEndpoint,
		engine.OTelTracesProtocolEnv+"="+otelProto,
		engine.OTelTracesEndpointEnv+"="+otelEndpoint+"/v1/traces",
		// Indicate that the /v1/trace endpoint accepts live telemetry.
		engine.OTelTracesLiveEnv+"=1",
		// Dagger sets up log+metric exporters too. Explicitly set them
		// so things can detect support for it.
		engine.OTelLogsProtocolEnv+"="+otelProto,
		engine.OTelLogsEndpointEnv+"="+otelEndpoint+"/v1/logs",
		engine.OTelMetricsProtocolEnv+"="+otelProto,
		engine.OTelMetricsEndpointEnv+"="+otelEndpoint+"/v1/metrics",
	)

	// Telemetry propagation (traceparent, tracestate, baggage, etc)
	state.spec.Process.Env = append(state.spec.Process.Env,
		telemetry.PropagationEnv(ctx)...)

	return nil
}

func (w *Worker) setupSecretScrubbing(ctx context.Context, state *execState) error {
	if w.execMD == nil {
		return nil
	}
	if len(w.execMD.SecretEnvNames) == 0 && len(w.execMD.SecretFilePaths) == 0 {
		return nil
	}

	ctrCwd := state.spec.Process.Cwd
	if ctrCwd == "" {
		ctrCwd = "/"
	}
	if !path.IsAbs(ctrCwd) {
		ctrCwd = filepath.Join("/", ctrCwd)
	}

	var secretFilePaths []string
	for _, filePath := range w.execMD.SecretFilePaths {
		if !path.IsAbs(filePath) {
			filePath = filepath.Join(ctrCwd, filePath)
		}
		var err error
		filePath, err = fs.RootPath(state.rootfsPath, filePath)
		if err != nil {
			return fmt.Errorf("secret file path %s points to invalid target: %w", filePath, err)
		}
		if _, err := os.Stat(filePath); err == nil {
			secretFilePaths = append(secretFilePaths, filePath)
		} else if !os.IsNotExist(err) {
			bklog.G(ctx).Warnf("failed to stat secret file path %s: %v", filePath, err)
		}
	}

	stdoutR, stdoutW := io.Pipe()
	stdoutScrubReader, err := NewSecretScrubReader(stdoutR, state.spec.Process.Env, w.execMD.SecretEnvNames, secretFilePaths)
	if err != nil {
		return fmt.Errorf("setup stdout secret scrubbing: %w", err)
	}
	stderrR, stderrW := io.Pipe()
	stderrScrubReader, err := NewSecretScrubReader(stderrR, state.spec.Process.Env, w.execMD.SecretEnvNames, secretFilePaths)
	if err != nil {
		return fmt.Errorf("setup stderr secret scrubbing: %w", err)
	}

	var pipeWg sync.WaitGroup

	finalStdout := state.procInfo.Stdout
	state.procInfo.Stdout = stdoutW
	pipeWg.Add(1)
	go func() {
		defer pipeWg.Done()
		io.Copy(finalStdout, stdoutScrubReader)
	}()

	finalStderr := state.procInfo.Stderr
	state.procInfo.Stderr = stderrW
	pipeWg.Add(1)
	go func() {
		defer pipeWg.Done()
		io.Copy(finalStderr, stderrScrubReader)
	}()

	state.cleanups.Add("close secret scrub stderr reader", stderrR.Close)
	state.cleanups.Add("close secret scrub stdout reader", stdoutR.Close)
	state.cleanups.Add("wait for secret scrubber pipes", cleanups.Infallible(pipeWg.Wait))
	state.cleanups.Add("close secret scrub stderr writer", stderrW.Close)
	state.cleanups.Add("close secret scrub stdout writer", stdoutW.Close)

	return nil
}

func (w *Worker) setProxyEnvs(_ context.Context, state *execState) error {
	for _, upperProxyEnvName := range engine.ProxyEnvNames {
		upperProxyVal, upperSet := state.origEnvMap[upperProxyEnvName]

		lowerProxyEnvName := strings.ToLower(upperProxyEnvName)
		lowerProxyVal, lowerSet := state.origEnvMap[lowerProxyEnvName]

		// try to set both upper and lower case proxy env vars, some programs
		// only respect one or the other
		switch {
		case upperSet && lowerSet:
			// both were already set explicitly by the user, don't overwrite
			continue
		case upperSet:
			// upper case was set, set lower case to the same value
			state.spec.Process.Env = append(state.spec.Process.Env, lowerProxyEnvName+"="+upperProxyVal)
		case lowerSet:
			// lower case was set, set upper case to the same value
			state.spec.Process.Env = append(state.spec.Process.Env, upperProxyEnvName+"="+lowerProxyVal)
		default:
			// neither was set by the user, check if the engine itself has the upper case
			// set and pass that through to the container in both cases if so
			val, ok := os.LookupEnv(upperProxyEnvName)
			if ok {
				state.spec.Process.Env = append(state.spec.Process.Env, upperProxyEnvName+"="+val, lowerProxyEnvName+"="+val)
			}
		}
	}

	if w.execMD == nil {
		return nil
	}

	const systemEnvPrefix = "_DAGGER_ENGINE_SYSTEMENV_"
	for _, systemEnvName := range w.execMD.SystemEnvNames {
		if _, ok := state.origEnvMap[systemEnvName]; ok {
			// don't overwrite explicit user-provided values
			continue
		}
		systemVal, ok := os.LookupEnv(systemEnvPrefix + systemEnvName)
		if ok {
			state.spec.Process.Env = append(state.spec.Process.Env, systemEnvName+"="+systemVal)
		}
	}

	return nil
}

func (w *Worker) enableGPU(_ context.Context, state *execState) error {
	if w.execMD == nil {
		return nil
	}
	if len(w.execMD.EnabledGPUs) == 0 {
		return nil
	}

	if state.spec.Hooks == nil {
		state.spec.Hooks = &specs.Hooks{}
	}
	//nolint:staticcheck
	state.spec.Hooks.Prestart = append(state.spec.Hooks.Prestart, specs.Hook{
		Args: []string{
			"nvidia-container-runtime-hook",
			"prestart",
		},
		Path: "/usr/bin/nvidia-container-runtime-hook",
	})
	state.spec.Process.Env = append(state.spec.Process.Env, fmt.Sprintf("NVIDIA_VISIBLE_DEVICES=%s",
		strings.Join(w.execMD.EnabledGPUs, ","),
	))

	return nil
}

func (w *Worker) createCWD(_ context.Context, state *execState) error {
	newp, err := fs.RootPath(state.rootfsPath, state.procInfo.Meta.Cwd)
	if err != nil {
		return fmt.Errorf("working dir %s points to invalid target: %w", newp, err)
	}
	if _, err := os.Stat(newp); err != nil {
		if err := user.MkdirAllAndChown(newp, 0o755, int(state.uid), int(state.gid), user.WithOnlyNew); err != nil {
			return fmt.Errorf("failed to create working directory %s: %w", newp, err)
		}
	}

	return nil
}

func (w *Worker) setupNestedClient(ctx context.Context, state *execState) (rerr error) {
	if w.execMD == nil {
		return nil
	}
	if w.execMD.ClientID == "" {
		return nil
	}

	clientIDPath := filepath.Join(state.metaMount.Source, MetaMountClientIDPath)
	if err := os.WriteFile(clientIDPath, []byte(w.execMD.ClientID), 0o600); err != nil {
		return fmt.Errorf("failed to write client id %s to %s: %w", w.execMD.ClientID, clientIDPath, err)
	}

	if w.execMD.SecretToken == "" {
		w.execMD.SecretToken = randid.NewID()
	}
	if w.execMD.Hostname == "" {
		w.execMD.Hostname = state.spec.Hostname
	}

	// propagate trace ctx to session attachables
	ctx = trace.ContextWithSpanContext(ctx, w.causeCtx)

	state.spec.Process.Env = append(state.spec.Process.Env, DaggerSessionTokenEnv+"="+w.execMD.SecretToken)

	w.execMD.ClientStableID = randid.NewID()

	// include SSH_AUTH_SOCK if it's set in the exec's env vars
	if sockPath, ok := state.origEnvMap["SSH_AUTH_SOCK"]; ok {
		if strings.HasPrefix(sockPath, "~") {
			if homeDir, ok := state.origEnvMap["HOME"]; ok {
				expandedPath, err := pathutil.ExpandHomeDir(homeDir, sockPath)
				if err != nil {
					return fmt.Errorf("failed to expand homedir: %w", err)
				}
				w.execMD.SSHAuthSocketPath = expandedPath
			} else {
				return fmt.Errorf("HOME not set, cannot expand SSH_AUTH_SOCK path: %s", sockPath)
			}
		} else {
			w.execMD.SSHAuthSocketPath = sockPath
		}
	}

	// include overridden client version if it's set in the exec's env vars
	if version, ok := state.origEnvMap["_EXPERIMENTAL_DAGGER_VERSION"]; ok {
		w.execMD.ClientVersionOverride = version
	}

	srvCtx, srvCancel := context.WithCancelCause(ctx)
	state.cleanups.Add("cancel session server", cleanups.Infallible(func() {
		srvCancel(errors.New("container cleanup"))
	}))
	srvPool := pool.New().WithContext(srvCtx).WithCancelOnError()

	httpListener, err := runInNetNS(ctx, state, func() (net.Listener, error) {
		return net.Listen("tcp", "127.0.0.1:0")
	})
	if err != nil {
		return fmt.Errorf("listen for nested client: %w", err)
	}
	state.cleanups.Add("close nested client listener", cleanups.IgnoreErrs(httpListener.Close, net.ErrClosed))

	tcpAddr, ok := httpListener.Addr().(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("unexpected listener address type: %T", httpListener.Addr())
	}
	state.spec.Process.Env = append(state.spec.Process.Env, DaggerSessionPortEnv+"="+strconv.Itoa(tcpAddr.Port))

	http2Srv := &http2.Server{}
	httpSrv := &http.Server{
		ReadHeaderTimeout: 10 * time.Second,
		Handler: h2c.NewHandler(http.HandlerFunc(func(resp http.ResponseWriter, req *http.Request) {
			w.sessionHandler.ServeHTTPToNestedClient(resp, req, w.execMD)
		}), http2Srv),
	}
	if err := http2.ConfigureServer(httpSrv, http2Srv); err != nil {
		return fmt.Errorf("configure nested client http2 server: %w", err)
	}

	srvPool.Go(func(_ context.Context) error {
		err := httpSrv.Serve(httpListener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			return fmt.Errorf("serve nested client listener: %w", err)
		}
		return nil
	})

	state.cleanups.Add("wait for nested client server pool", srvPool.Wait)
	// state.cleanups.ReAdd(stopSessionSrv)
	state.cleanups.Add("close nested client http server", httpSrv.Close)
	state.cleanups.Add("cancel nested client server pool", cleanups.Infallible(func() {
		srvCancel(errors.New("container cleanup"))
	}))

	return nil
}

func (w *Worker) installCACerts(ctx context.Context, state *execState) error {
	caInstaller, err := cacerts.NewInstaller(ctx, state.spec, func(ctx context.Context, args ...string) error {
		output := new(bytes.Buffer)
		caExecState := &execState{
			id: randid.NewID(),
			procInfo: &executor.ProcessInfo{
				Stdout: nopCloser{output},
				Stderr: nopCloser{output},
				Meta: executor.Meta{
					Args: args,
					Env:  state.spec.Process.Env,
					User: "0:0",
					Cwd:  "/",
				},
			},
			rootMount: state.rootMount,
			mounts:    state.mounts,

			cleanups: &cleanups.Cleanups{},

			spec:             &specs.Spec{},
			networkNamespace: state.networkNamespace,
			rootfsPath:       state.rootfsPath,
			resolvConfPath:   state.resolvConfPath,
			hostsFilePath:    state.hostsFilePath,

			startedOnce: &sync.Once{},
			startedCh:   make(chan struct{}),

			done: make(chan struct{}),
		}

		// copy the spec by doing a json ser/deser round (this could be more efficient, but
		// probably not a bottleneck)
		bs, err := json.Marshal(state.spec)
		if err != nil {
			return fmt.Errorf("marshal spec: %w", err)
		}
		if err := json.Unmarshal(bs, caExecState.spec); err != nil {
			return fmt.Errorf("unmarshal spec: %w", err)
		}
		caExecState.spec.Process.Args = caExecState.procInfo.Meta.Args
		caExecState.spec.Process.User.UID = 0
		caExecState.spec.Process.User.GID = 0
		caExecState.spec.Process.Cwd = "/"
		caExecState.spec.Process.Terminal = false

		if err := w.run(ctx, caExecState, w.runContainer); err != nil {
			return fmt.Errorf("installer command failed: %w, output: %s", err, output.String())
		}
		return nil
	})
	if err != nil {
		bklog.G(ctx).Errorf("failed to create cacerts installer, falling back to not installing CA certs: %v", err)
		return nil
	}

	err = caInstaller.Install(ctx)
	switch {
	case err == nil:
		state.cleanups.Add("uninstall CA certs", func() error {
			return caInstaller.Uninstall(ctx)
		})
	case errors.As(err, new(cacerts.CleanupErr)):
		// if install failed and cleanup failed too, we have no choice but to fail this exec; otherwise we're
		// leaving the container in some weird half state
		return fmt.Errorf("failed to install cacerts: %w", err)
	default:
		// if install failed but we were able to cleanup, then we should log it but don't need to fail the exec
		bklog.G(ctx).Errorf("failed to install cacerts but successfully cleaned up, continuing without CA certs: %v", err)
	}

	return nil
}

func (w *Worker) runContainer(ctx context.Context, state *execState) (rerr error) {
	bundle := filepath.Join(w.executorRoot, state.id)
	if err := os.Mkdir(bundle, 0o711); err != nil {
		return err
	}
	state.cleanups.Add("remove bundle", func() error {
		return os.RemoveAll(bundle)
	})

	configPath := filepath.Join(bundle, "config.json")
	f, err := os.Create(configPath)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := json.NewEncoder(f).Encode(state.spec); err != nil {
		return fmt.Errorf("failed to encode spec: %w", err)
	}
	f.Close()

	lg := bklog.G(ctx).
		WithField("id", state.id).
		WithField("args", state.spec.Process.Args)
	if w.execMD != nil && w.execMD.CallerClientID != "" {
		lg = lg.WithField("caller_client_id", w.execMD.CallerClientID)
		if w.execMD.CallID != nil {
			lg = lg.WithField("call_id", w.execMD.CallID.Digest())
		}
		if w.execMD.ClientID != "" {
			lg = lg.WithField("nested_client_id", w.execMD.ClientID)
		}
	}
	lg.Debug("starting container")
	defer func() {
		lg.WithError(rerr).Debug("container done")
	}()

	trace.SpanFromContext(ctx).AddEvent("Container created")

	state.cleanups.Add("runc delete container", func() error {
		return w.runc.Delete(context.WithoutCancel(ctx), state.id, &runc.DeleteOpts{})
	})

	cgroupPath := state.spec.Linux.CgroupsPath
	if cgroupPath != "" && w.execMD != nil && w.execMD.CallID != nil {
		meter := telemetry.Meter(ctx, InstrumentationLibrary)

		commonAttrs := []attribute.KeyValue{
			attribute.String(telemetry.DagDigestAttr, string(w.execMD.CallID.Digest())),
		}
		spanContext := trace.SpanContextFromContext(ctx)
		if spanContext.HasSpanID() {
			commonAttrs = append(commonAttrs,
				attribute.String(telemetry.MetricsSpanIDAttr, spanContext.SpanID().String()),
			)
		}
		if spanContext.HasTraceID() {
			commonAttrs = append(commonAttrs,
				attribute.String(telemetry.MetricsTraceIDAttr, spanContext.TraceID().String()),
			)
		}

		cgroupSampler, err := resources.NewSampler(cgroupPath, state.networkNamespace, meter, attribute.NewSet(commonAttrs...))
		if err != nil {
			return fmt.Errorf("create cgroup sampler: %w", err)
		}

		cgroupSamplerCtx, cgroupSamplerCancel := context.WithCancelCause(context.WithoutCancel(ctx))
		cgroupSamplerPool := pool.New()

		state.cleanups.Add("cancel cgroup sampler", cleanups.Infallible(func() {
			cgroupSamplerCancel(fmt.Errorf("container cleanup: %w", context.Canceled))
			cgroupSamplerPool.Wait()
		}))

		cgroupSamplerPool.Go(func() {
			ticker := time.NewTicker(cgroupSampleInterval)
			defer ticker.Stop()

			for {
				select {
				case <-cgroupSamplerCtx.Done():
					// try a quick final sample before closing
					finalCtx, finalCancel := context.WithTimeout(context.WithoutCancel(cgroupSamplerCtx), finalCgroupSampleTimeout)
					defer finalCancel()
					if err := cgroupSampler.Sample(finalCtx); err != nil {
						bklog.G(ctx).Error("failed to sample cgroup after cancel", "err", err)
					}

					return
				case <-ticker.C:
					if err := cgroupSampler.Sample(cgroupSamplerCtx); err != nil {
						bklog.G(ctx).Error("failed to sample cgroup", "err", err)
					}
				}
			}
		})
	}

	startedCallback := func() {
		state.startedOnce.Do(func() {
			trace.SpanFromContext(ctx).AddEvent("Container started")
			if state.startedCh != nil {
				close(state.startedCh)
			}
		})
	}

	killer := newRunProcKiller(w.runc, state.id)

	runcCall := func(ctx context.Context, started chan<- int, io runc.IO, pidfile string) error {
		_, err := w.runc.Run(ctx, state.id, bundle, &runc.CreateOpts{
			Started:   started,
			IO:        io,
			ExtraArgs: []string{"--keep"},
		})
		return err
	}

	return exitError(ctx, state.exitCodePath, w.callWithIO(ctx, state.procInfo, startedCallback, killer, runcCall), state.procInfo.Meta.ValidExitCodes)
}
