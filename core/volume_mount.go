package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	ctrdmount "github.com/containerd/containerd/v2/core/mount"
	ctrdfs "github.com/containerd/continuity/fs"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/internal/buildkit/identity"
	"github.com/moby/sys/userns"
	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
)

func prepareExecVolumeMount(ctx context.Context, cfg *execVolumeMountConfig) (bkcache.Mountable, error) {
	if cfg == nil {
		return nil, fmt.Errorf("invalid volume mount options")
	}
	if cfg.Volume.Self() == nil {
		return nil, fmt.Errorf("volume mount missing volume")
	}
	vol := cfg.Volume.Self()
	switch vol.Backend {
	case VolumeBackendKindEngine:
		if vol.Engine == nil {
			return nil, fmt.Errorf("engine volume mount missing config")
		}
		query, err := CurrentQuery(ctx)
		if err != nil {
			return nil, fmt.Errorf("get current query for engine volume: %w", err)
		}
		state := query.EngineVolumeState()
		if state.RootDir == "" {
			return nil, fmt.Errorf("engine volume state root is not configured")
		}
		return &execEngineVolumeMount{cfg: *vol.Engine, state: state}, nil
	case VolumeBackendKindSSHFS:
		if vol.SSHFS == nil {
			return nil, fmt.Errorf("SSHFS volume mount missing config")
		}
		return &execVolumeMount{volume: cfg.Volume}, nil
	default:
		return nil, fmt.Errorf("unsupported volume backend %q", vol.Backend)
	}
}

type execEngineVolumeMount struct {
	cfg   EngineVolumeConfig
	state EngineVolumeState
}

func (mnt *execEngineVolumeMount) Mount(_ context.Context, readonly bool) (bkcache.MountableRef, error) {
	source, err := prepareEngineVolumeSource(mnt.state.RootDir, &mnt.cfg)
	if err != nil {
		return nil, err
	}

	options := []string{"rbind"}
	if readonly {
		if mnt.state.RecursiveReadOnlySupported {
			options = append(options, "rro")
		} else {
			options = append(options, "ro")
		}
	}
	return &execVolumeMountInstance{mounts: []ctrdmount.Mount{{
		Type:    "bind",
		Source:  source,
		Options: options,
	}}, release: func() error { return nil }}, nil
}

var engineVolumeCreateMu sync.Mutex

func prepareEngineVolumeSource(engineRoot string, cfg *EngineVolumeConfig) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("engine volume config is missing")
	}
	if err := validateEngineVolumeConfig(cfg); err != nil {
		return "", fmt.Errorf("invalid engine volume %q: %w", cfg.Name, err)
	}
	if !filepath.IsAbs(engineRoot) {
		return "", fmt.Errorf("engine volume state root %q is not absolute", engineRoot)
	}

	// Serialize Dagger's own first-use creation so every successful caller sees
	// the exact requested mode without changing pre-existing operator objects.
	engineVolumeCreateMu.Lock()
	defer engineVolumeCreateMu.Unlock()

	namespaceRoot, err := ctrdfs.RootPath(engineRoot, filepath.Join("volumes", "v1"))
	if err != nil {
		return "", fmt.Errorf("resolve engine volume namespace: %w", err)
	}
	if err := ensureEngineVolumeDirs(engineRoot, namespaceRoot); err != nil {
		return "", fmt.Errorf("prepare engine volume namespace: %w", err)
	}

	volumeRoot, err := ctrdfs.RootPath(namespaceRoot, filepath.Join(filepath.FromSlash(cfg.Name), "fs"))
	if err != nil {
		return "", fmt.Errorf("resolve engine volume %q: %w", cfg.Name, err)
	}
	if err := ensureEngineVolumeDirs(namespaceRoot, volumeRoot); err != nil {
		return "", fmt.Errorf("prepare engine volume %q: %w", cfg.Name, err)
	}

	selectedRoot := volumeRoot
	if cfg.Subdir != "" {
		selectedRoot, err = ctrdfs.RootPath(volumeRoot, filepath.FromSlash(cfg.Subdir))
		if err != nil {
			return "", fmt.Errorf("resolve engine volume %q subdir %q: %w", cfg.Name, cfg.Subdir, err)
		}
		info, statErr := os.Stat(selectedRoot)
		switch {
		case os.IsNotExist(statErr):
			return "", fmt.Errorf("engine volume %q subdir %q does not exist", cfg.Name, cfg.Subdir)
		case statErr != nil:
			return "", fmt.Errorf("stat engine volume %q subdir %q: %w", cfg.Name, cfg.Subdir, statErr)
		case !info.IsDir():
			return "", fmt.Errorf("engine volume %q subdir %q is not a directory", cfg.Name, cfg.Subdir)
		}
	}
	return selectedRoot, nil
}

func ensureEngineVolumeDirs(root, target string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if rel == "." {
		return requireEngineVolumeDir(root)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("path %q is outside root %q", target, root)
	}

	current := root
	if err := requireEngineVolumeDir(current); err != nil {
		return err
	}
	for _, component := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, component)
		err := os.Mkdir(current, 0o755)
		switch {
		case err == nil:
			// Mkdir honors the process umask. Only chmod objects this call
			// created; an existing operator object is never modified.
			if err := os.Chmod(current, 0o755); err != nil {
				return fmt.Errorf("set mode on new directory %q: %w", current, err)
			}
		case os.IsExist(err):
			// Preserve existing modes and ownership.
		default:
			return fmt.Errorf("create directory %q: %w", current, err)
		}
		if err := requireEngineVolumeDir(current); err != nil {
			return err
		}
	}
	return nil
}

func requireEngineVolumeDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %q: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%q is not a directory", path)
	}
	return nil
}

type execVolumeMount struct {
	volume dagql.ObjectResult[*Volume]
}

func (mnt *execVolumeMount) Mount(ctx context.Context, readonly bool) (bkcache.MountableRef, error) {
	mounts, release, err := mountSSHFSVolume(ctx, readonly, mnt.volume.Self().SSHFS)
	if err != nil {
		return nil, err
	}
	return &execVolumeMountInstance{
		mounts:  mounts,
		release: release,
	}, nil
}

type execVolumeMountInstance struct {
	mounts  []ctrdmount.Mount
	release func() error
}

func (mnt *execVolumeMountInstance) Mount() ([]ctrdmount.Mount, func() error, error) {
	return mnt.mounts, mnt.release, nil
}

func mountSSHFSVolume(ctx context.Context, readonly bool, cfg *SSHFSVolumeConfig) (_ []ctrdmount.Mount, _ func() error, rerr error) {
	privateKey, err := plaintextSecret(ctx, cfg.PrivateKey, "volume private key")
	if err != nil {
		return nil, nil, err
	}

	var knownHosts []byte
	if cfg.KnownHosts.Self() != nil {
		knownHosts, err = plaintextSecret(ctx, cfg.KnownHosts, "volume known hosts")
		if err != nil {
			return nil, nil, err
		}
		if len(knownHosts) == 0 && !cfg.InsecureSkipHostKeyCheck {
			return nil, nil, fmt.Errorf("volume known hosts empty")
		}
	} else if !cfg.InsecureSkipHostKeyCheck {
		return nil, nil, fmt.Errorf("volume known hosts missing")
	}

	source, port, releaseService, err := sshfsMountSource(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	release := releaseService
	defer func() {
		if rerr != nil && release != nil {
			_ = runSSHFSVolumeCleanup(ctx, release)
		}
	}()

	workDir, err := os.MkdirTemp("", "dagger-sshfs-")
	if err != nil {
		return nil, nil, fmt.Errorf("create sshfs workdir: %w", err)
	}
	release = joinCleanup(func() error {
		return os.RemoveAll(workDir)
	}, release)

	tmpMount := ctrdmount.Mount{
		Type:    "tmpfs",
		Source:  "tmpfs",
		Options: []string{"nodev", "nosuid", "mode=0700", fmt.Sprintf("uid=%d,gid=%d", os.Geteuid(), os.Getegid())},
	}
	if userns.RunningInUserNS() {
		tmpMount.Options = nil
	}
	if err = ctrdmount.All([]ctrdmount.Mount{tmpMount}, workDir); err != nil {
		return nil, nil, fmt.Errorf("mount sshfs workdir tmpfs: %w", err)
	}
	// Cleanup is ordered outside-in: unmount the sshfs mount, unmount the tmpfs
	// holding key material, remove the temp dir, then release any backing service.
	release = joinCleanup(unmountWithDetachFallback(workDir), release)

	mountDir := filepath.Join(workDir, "mnt")
	if err = os.Mkdir(mountDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create sshfs mountpoint: %w", err)
	}
	keyPath := filepath.Join(workDir, identity.NewID())
	if err = os.WriteFile(keyPath, privateKey, 0o600); err != nil {
		return nil, nil, fmt.Errorf("write sshfs private key: %w", err)
	}

	var knownHostsPath string
	if len(knownHosts) > 0 {
		knownHostsPath = filepath.Join(workDir, identity.NewID())
		if err = os.WriteFile(knownHostsPath, knownHosts, 0o600); err != nil {
			return nil, nil, fmt.Errorf("write sshfs known_hosts: %w", err)
		}
	}

	args := sshfsCommandArgs(source, mountDir, sshfsCommandConfig{
		Port:                     port,
		PrivateKeyPath:           keyPath,
		KnownHostsPath:           knownHostsPath,
		HostKeyAlias:             cfg.HostKeyAlias,
		InsecureSkipHostKeyCheck: cfg.InsecureSkipHostKeyCheck,
		AllowOther:               sshfsAllowOther(),
		Readonly:                 readonly,
	})
	var stderr bytes.Buffer
	cmd := osexec.CommandContext(ctx, "sshfs", args...)
	cmd.Stderr = &stderr
	if err = runProcessGroup(ctx, cmd); err != nil {
		_ = unmountWithDetachFallback(mountDir)()
		return nil, nil, fmt.Errorf("mount sshfs volume: %w%s", err, formatCommandStderr(stderr.Bytes()))
	}

	// sshfs must daemonize here. runProcessGroup waits for the parent sshfs
	// process to report mount readiness, while the detached FUSE daemon owns the
	// mount until the executor calls the release function below.
	release = joinCleanup(unmountWithDetachFallback(mountDir), release)
	bindOptions := []string{"rbind"}
	if readonly {
		bindOptions = append(bindOptions, "ro")
	}
	mounts := []ctrdmount.Mount{{
		Type:    "bind",
		Source:  mountDir,
		Options: bindOptions,
	}}
	return mounts, func() error {
		return runSSHFSVolumeCleanup(ctx, release)
	}, nil
}

type sshfsCommandConfig struct {
	Port                     string
	PrivateKeyPath           string
	KnownHostsPath           string
	HostKeyAlias             string
	InsecureSkipHostKeyCheck bool
	AllowOther               bool
	Readonly                 bool
}

func sshfsAllowOther() bool {
	if os.Geteuid() == 0 && !userns.RunningInUserNS() {
		return true
	}
	fuseConf, err := os.ReadFile("/etc/fuse.conf")
	if err != nil {
		return false
	}
	return fuseConfAllowsOther(fuseConf)
}

func fuseConfAllowsOther(fuseConf []byte) bool {
	for _, line := range strings.Split(string(fuseConf), "\n") {
		line, _, _ = strings.Cut(line, "#")
		if strings.TrimSpace(line) == "user_allow_other" {
			return true
		}
	}
	return false
}

func sshfsCommandArgs(source, mountDir string, cfg sshfsCommandConfig) []string {
	args := []string{source, mountDir}
	if cfg.Port != "" {
		args = append(args, "-p", cfg.Port)
	}
	args = append(args,
		"-o", "BatchMode=yes",
		"-o", "IdentitiesOnly=yes",
		"-o", "IdentityFile="+cfg.PrivateKeyPath,
	)
	if cfg.AllowOther {
		args = append(args, "-o", "allow_other")
	}
	if cfg.InsecureSkipHostKeyCheck {
		args = append(args,
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
		)
	} else {
		args = append(args,
			"-o", "StrictHostKeyChecking=yes",
			"-o", "UserKnownHostsFile="+cfg.KnownHostsPath,
		)
		if cfg.HostKeyAlias != "" {
			args = append(args, "-o", "HostKeyAlias="+cfg.HostKeyAlias)
		}
	}
	if cfg.Readonly {
		args = append(args, "-o", "ro")
	}
	return args
}

func sshfsMountSource(ctx context.Context, cfg *SSHFSVolumeConfig) (string, string, func() error, error) {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil {
		return "", "", nil, fmt.Errorf("parse sshfs endpoint: %w", err)
	}
	host := u.Hostname()
	port := u.Port()
	var release func() error
	if cfg.ServiceHost.Self() != nil {
		running, releaseService, err := startSSHFSServiceHost(ctx, cfg.ServiceHost)
		if err != nil {
			return "", "", nil, err
		}
		release = releaseService
		// Service-backed SSHFS connects to the running service address, while
		// HostKeyAlias continues to verify the logical endpoint host.
		host = running.Host
		port, err = sshfsServiceHostPort(running, port)
		if err != nil {
			if release != nil {
				_ = release()
			}
			return "", "", nil, err
		}
	}
	source, err := sshfsSourceForURL(u, host)
	if err != nil {
		if release != nil {
			_ = release()
		}
		return "", "", nil, err
	}
	return source, port, release, nil
}

func startSSHFSServiceHost(ctx context.Context, service dagql.ObjectResult[*Service]) (*RunningService, func() error, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("get current query for sshfs service host: %w", err)
	}
	svcs, err := query.Services(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("get services for sshfs service host: %w", err)
	}
	running, release, err := svcs.StartResultWithDependencyExitPropagationSuppressed(ctx, service)
	if err != nil {
		return nil, nil, fmt.Errorf("start sshfs service host: %w", err)
	}
	return running, func() error {
		release()
		return nil
	}, nil
}

func sshfsServiceHostPort(running *RunningService, endpointPort string) (string, error) {
	if len(running.Ports) == 0 {
		return "", fmt.Errorf("sshfs service host exposes no ports")
	}
	if endpointPort != "" {
		port, err := strconv.Atoi(endpointPort)
		if err != nil {
			return "", fmt.Errorf("parse sshfs endpoint port %q: %w", endpointPort, err)
		}
		for _, exposed := range running.Ports {
			if exposed.Port == port {
				if exposed.Protocol != "" && exposed.Protocol != NetworkProtocolTCP {
					return "", fmt.Errorf("sshfs service host endpoint port %s is not TCP", endpointPort)
				}
				return endpointPort, nil
			}
		}
		return "", fmt.Errorf("sshfs service host does not expose endpoint port %s", endpointPort)
	}

	var tcpPorts []Port
	for _, exposed := range running.Ports {
		if exposed.Protocol == "" || exposed.Protocol == NetworkProtocolTCP {
			tcpPorts = append(tcpPorts, exposed)
		}
	}
	if len(tcpPorts) == 0 {
		return "", fmt.Errorf("sshfs service host exposes no TCP ports")
	}
	if len(tcpPorts) > 1 {
		return "", fmt.Errorf("sshfs service host exposes multiple TCP ports; include the SSH port in the endpoint")
	}
	return strconv.Itoa(tcpPorts[0].Port), nil
}

func sshfsSourceForURL(u *url.URL, host string) (string, error) {
	user := ""
	if u.User != nil {
		user = u.User.Username()
	}
	if user == "" {
		return "", fmt.Errorf("sshfs endpoint missing user")
	}
	if host == "" {
		return "", fmt.Errorf("sshfs endpoint missing host")
	}
	if strings.HasPrefix(host, "-") {
		return "", fmt.Errorf("sshfs endpoint host must not start with '-'")
	}
	path := u.Path
	if path == "" || !strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("sshfs endpoint missing absolute path")
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("%s@%s:%s", user, host, path), nil
}

func plaintextSecret(ctx context.Context, secret dagql.ObjectResult[*Secret], label string) ([]byte, error) {
	if secret.Self() == nil {
		return nil, fmt.Errorf("%s missing", label)
	}
	plaintext, err := secret.Self().Plaintext(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	return plaintext, nil
}

func unmountWithDetachFallback(dir string) func() error {
	return func() error {
		if err := ctrdmount.Unmount(dir, 0); err != nil {
			if detachErr := ctrdmount.Unmount(dir, unix.MNT_DETACH); detachErr != nil {
				return errors.Join(err, detachErr)
			}
		}
		return nil
	}
}

func joinCleanup(first, second func() error) func() error {
	return func() error {
		return errors.Join(callCleanup(first), callCleanup(second))
	}
}

func callCleanup(cleanup func() error) error {
	if cleanup == nil {
		return nil
	}
	return cleanup()
}

// Mount releaser errors are not propagated after an exec completes, so log
// SSHFS cleanup failures here while still returning them to the caller.
func runSSHFSVolumeCleanup(ctx context.Context, cleanup func() error) error {
	err := callCleanup(cleanup)
	if err != nil {
		slog.ErrorContext(ctx, "failed to clean up SSHFS volume", "error", err)
	}
	return err
}

func formatCommandStderr(stderr []byte) string {
	trimmed := strings.TrimSpace(string(stderr))
	if trimmed == "" {
		return ""
	}
	return ": " + trimmed
}
