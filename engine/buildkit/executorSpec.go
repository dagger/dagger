package buildkit

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/containerd/containerd/mount"
	ctdoci "github.com/containerd/containerd/oci"
	"github.com/containerd/continuity/fs"
	"github.com/docker/docker/pkg/idtools"
	"github.com/moby/buildkit/executor"
	"github.com/moby/buildkit/executor/oci"
	randid "github.com/moby/buildkit/identity"
	bksession "github.com/moby/buildkit/session"
	"github.com/moby/buildkit/util/bklog"
	bknetwork "github.com/moby/buildkit/util/network"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/sourcegraph/conc/pool"
	"go.opentelemetry.io/otel/propagation"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit/cacerts"
	"github.com/dagger/dagger/engine/buildkit/containerfs"
	"github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/session"
	"github.com/dagger/dagger/network"
	"github.com/dagger/dagger/telemetry"
)

const (
	DaggerServerIDEnv        = "_DAGGER_SERVER_ID"
	DaggerClientIDEnv        = "_DAGGER_NESTED_CLIENT_ID"
	DaggerCallDigestEnv      = "_DAGGER_CALL_DIGEST"
	DaggerEngineVersionEnv   = "_DAGGER_ENGINE_VERSION"
	DaggerRedirectStdoutEnv  = "_DAGGER_REDIRECT_STDOUT"
	DaggerRedirectStderrEnv  = "_DAGGER_REDIRECT_STDERR"
	DaggerHostnameAliasesEnv = "_DAGGER_HOSTNAME_ALIASES"

	DaggerSessionPortEnv  = "DAGGER_SESSION_PORT"
	DaggerSessionTokenEnv = "DAGGER_SESSION_TOKEN"

	// this is set by buildkit, we cannot change
	BuildkitSessionIDHeader = "x-docker-expose-session-uuid"

	OTELTraceParentEnv      = "TRACEPARENT"
	OTELExporterProtocolEnv = "OTEL_EXPORTER_OTLP_PROTOCOL"
	OTELExporterEndpointEnv = "OTEL_EXPORTER_OTLP_ENDPOINT"
	OTELTracesProtocolEnv   = "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"
	OTELTracesEndpointEnv   = "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"
	OTELLogsProtocolEnv     = "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL"
	OTELLogsEndpointEnv     = "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"
	OTELMetricsProtocolEnv  = "OTEL_EXPORTER_OTLP_METRICS_PROTOCOL"
	OTELMetricsEndpointEnv  = "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"
)

var removeEnvs = map[string]struct{}{
	// envs that are used to scope cache but not needed at runtime
	DaggerCallDigestEnv:      {},
	DaggerEngineVersionEnv:   {},
	DaggerRedirectStdoutEnv:  {},
	DaggerRedirectStderrEnv:  {},
	DaggerHostnameAliasesEnv: {},
}

type spec struct {
	// should be set by the caller
	cleanups         *cleanups
	runState         *runningState
	procInfo         *executor.ProcessInfo
	rootfsPath       string
	rootMount        executor.Mount
	mounts           []executor.Mount
	id               string
	networkNamespace bknetwork.Namespace
	installCACerts   bool

	// will be set by the generator
	*specs.Spec
	uid             uint32
	gid             uint32
	sgids           []uint32
	resolvConfPath  string
	hostsFilePath   string
	exitCodePath    string
	metaMount       *specs.Mount
	otelTraceparent string
	otelEndpoint    string
	otelProto       string
	origEnvMap      map[string]string
	extraOpts       []ctdoci.SpecOpts
}

type executorSetupFunc func(context.Context, *spec) error

func (w *Worker) setUserGroup(_ context.Context, spec *spec) error {
	var err error
	spec.uid, spec.gid, spec.sgids, err = oci.GetUser(spec.rootfsPath, spec.procInfo.Meta.User)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}

	spec.extraOpts = append(spec.extraOpts, oci.WithUIDGID(spec.uid, spec.gid, spec.sgids))
	return nil
}

func (w *Worker) setupNetwork(ctx context.Context, spec *spec) error {
	var err error
	spec.resolvConfPath, err = oci.GetResolvConf(ctx, w.executorRoot, w.idmap, w.dns, spec.procInfo.Meta.NetMode)
	if err != nil {
		return fmt.Errorf("get base resolv.conf: %w", err)
	}

	var cleanupBaseHosts func()
	spec.hostsFilePath, cleanupBaseHosts, err = oci.GetHostsFile(
		ctx, w.executorRoot, spec.procInfo.Meta.ExtraHosts, w.idmap, spec.procInfo.Meta.Hostname)
	if err != nil {
		return fmt.Errorf("get base hosts file: %w", err)
	}
	if cleanupBaseHosts != nil {
		spec.cleanups.addNoErr("cleanup base hosts file", cleanupBaseHosts)
	}

	if w.execMD == nil || w.execMD.ServerID == "" {
		return nil
	}

	extraSearchDomain := network.ClientDomain(w.execMD.ServerID)

	baseResolvFile, err := os.Open(spec.resolvConfPath)
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
	spec.resolvConfPath = ctrResolvFile.Name()
	spec.cleanups.add("remove resolv.conf", func() error {
		return os.Remove(spec.resolvConfPath)
	})

	if err := ctrResolvFile.Chmod(baseResolvStat.Mode().Perm()); err != nil {
		return fmt.Errorf("chmod resolv.conf: %w", err)
	}

	scanner := bufio.NewScanner(baseResolvFile)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "search") {
			if _, err := fmt.Fprintln(ctrResolvFile, line); err != nil {
				return fmt.Errorf("write resolv.conf: %w", err)
			}
			continue
		}

		domains := append(strings.Fields(line)[1:], extraSearchDomain)
		if _, err := fmt.Fprintln(ctrResolvFile, "search", strings.Join(domains, " ")); err != nil {
			return fmt.Errorf("write resolv.conf: %w", err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read resolv.conf: %w", err)
	}

	if len(w.execMD.HostAliases) == 0 {
		return nil
	}

	baseHostsFile, err := os.Open(spec.hostsFilePath)
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
	spec.hostsFilePath = ctrHostsFile.Name()
	spec.cleanups.add("remove hosts file", func() error {
		return os.Remove(spec.hostsFilePath)
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
		for _, domain := range []string{"", extraSearchDomain} {
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

func (w *Worker) generateBaseSpec(ctx context.Context, spec *spec) error {
	if spec.procInfo.Meta.ReadonlyRootFS {
		spec.extraOpts = append(spec.extraOpts, ctdoci.WithRootFSReadonly())
	}

	baseSpec, ociSpecCleanup, err := oci.GenerateSpec(
		ctx,
		spec.procInfo.Meta,
		spec.mounts,
		spec.id,
		spec.resolvConfPath,
		spec.hostsFilePath,
		spec.networkNamespace,
		w.cgroupParent,
		w.processMode,
		w.idmap,
		w.apparmorProfile,
		w.selinux,
		w.tracingSocket,
		spec.extraOpts...,
	)
	if err != nil {
		return err
	}
	spec.cleanups.addNoErr("base OCI spec cleanup", ociSpecCleanup)

	spec.Spec = baseSpec
	return nil
}

func (w *Worker) setupNamespaces(ctx context.Context, spec *spec) error {
	spec.runState.namespaces = spec.Linux.Namespaces
	if err := w.runNamespaceWorkers(ctx, spec.runState, spec.cleanups); err != nil {
		return fmt.Errorf("failed to handle namespace jobs: %w", err)
	}
	return nil
}

func (w *Worker) filterEnvs(_ context.Context, spec *spec) error {
	spec.origEnvMap = make(map[string]string)
	filteredEnvs := make([]string, 0, len(spec.Process.Env))
	for _, env := range spec.Process.Env {
		k, v, ok := strings.Cut(env, "=")
		if !ok {
			continue
		}
		switch k {
		case OTELTracesEndpointEnv:
			spec.otelEndpoint = v
		case OTELTracesProtocolEnv:
			spec.otelProto = v
		default:
			if _, ok := removeEnvs[k]; !ok {
				spec.origEnvMap[k] = v
				filteredEnvs = append(filteredEnvs, env)
			}
		}
	}
	spec.Process.Env = filteredEnvs

	return nil
}

func (w *Worker) filterMetaMount(_ context.Context, spec *spec) error {
	var filteredMounts []specs.Mount
	for _, mnt := range spec.Mounts {
		if mnt.Destination == MetaMountDestPath {
			mnt := mnt
			spec.metaMount = &mnt
			continue
		}
		filteredMounts = append(filteredMounts, mnt)
	}
	spec.Mounts = filteredMounts

	return nil
}

func (w *Worker) configureRootfs(_ context.Context, spec *spec) error {
	spec.Root.Path = spec.rootfsPath
	if spec.rootMount.Readonly {
		spec.Root.Readonly = true
	}
	return nil
}

func (w *Worker) setExitCodePath(_ context.Context, spec *spec) error {
	if spec.metaMount != nil {
		spec.exitCodePath = filepath.Join(spec.metaMount.Source, MetaMountExitCodePath)
	}
	return nil
}

func (w *Worker) setupStdio(_ context.Context, spec *spec) error {
	if spec.procInfo.Meta.Tty {
		spec.Process.Terminal = true
		// no more stdio setup needed
		return nil
	}
	if spec.metaMount == nil {
		return nil
	}

	stdinPath := filepath.Join(spec.metaMount.Source, MetaMountStdinPath)
	stdinFile, err := os.Open(stdinPath)
	switch {
	case err == nil:
		spec.cleanups.add("close container stdin file", stdinFile.Close)
		spec.procInfo.Stdin = stdinFile
	case os.IsNotExist(err):
		// no stdin to send
	default:
		return fmt.Errorf("open stdin file: %w", err)
	}

	var stdoutWriters []io.Writer
	if spec.procInfo.Stdout != nil {
		stdoutWriters = append(stdoutWriters, spec.procInfo.Stdout)
	}
	stdoutPath := filepath.Join(spec.metaMount.Source, MetaMountStdoutPath)
	stdoutFile, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open stdout file: %w", err)
	}
	spec.cleanups.add("close container stdout file", stdoutFile.Close)
	stdoutWriters = append(stdoutWriters, stdoutFile)

	var stderrWriters []io.Writer
	if spec.procInfo.Stderr != nil {
		stderrWriters = append(stderrWriters, spec.procInfo.Stderr)
	}
	stderrPath := filepath.Join(spec.metaMount.Source, MetaMountStderrPath)
	stderrFile, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open stderr file: %w", err)
	}
	spec.cleanups.add("close container stderr file", stderrFile.Close)
	stderrWriters = append(stderrWriters, stderrFile)

	if w.execMD != nil && (w.execMD.RedirectStdoutPath != "" || w.execMD.RedirectStderrPath != "") {
		ctrFS, err := containerfs.NewContainerFS(spec.Spec, nil)
		if err != nil {
			return err
		}

		ctrCwd := spec.Process.Cwd
		if ctrCwd == "" {
			ctrCwd = "/"
		}
		if !path.IsAbs(ctrCwd) {
			ctrCwd = filepath.Join("/", ctrCwd)
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
			spec.cleanups.add("close redirect stdout file", redirectStdoutFile.Close)
			if err := redirectStdoutFile.Chown(int(spec.Process.User.UID), int(spec.Process.User.GID)); err != nil {
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
			spec.cleanups.add("close redirect stderr file", redirectStderrFile.Close)
			if err := redirectStderrFile.Chown(int(spec.Process.User.UID), int(spec.Process.User.GID)); err != nil {
				return fmt.Errorf("chown redirect stderr file: %w", err)
			}
			stderrWriters = append(stderrWriters, redirectStderrFile)
		}
	}

	spec.procInfo.Stdout = nopCloser{io.MultiWriter(stdoutWriters...)}
	spec.procInfo.Stderr = nopCloser{io.MultiWriter(stderrWriters...)}

	return nil
}

func (w *Worker) setupOTEL(ctx context.Context, spec *spec) error {
	if w.execMD != nil {
		spec.Process.Env = append(spec.Process.Env, w.execMD.OTELEnvs...)
	}

	if spec.otelEndpoint == "" {
		return nil
	}

	traceParent, ok := spec.origEnvMap[OTELTraceParentEnv]
	if ok {
		otelCtx := propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier{"traceparent": traceParent})
		otelLogger := telemetry.Logger("dagger.io/executor")
		stdout := &telemetry.OtelWriter{
			Ctx:    otelCtx,
			Logger: otelLogger,
			Stream: 1,
		}
		stderr := &telemetry.OtelWriter{
			Ctx:    otelCtx,
			Logger: otelLogger,
			Stream: 2,
		}
		spec.procInfo.Stdout = nopCloser{io.MultiWriter(stdout, spec.procInfo.Stdout)}
		spec.procInfo.Stderr = nopCloser{io.MultiWriter(stderr, spec.procInfo.Stderr)}
	}

	if strings.HasPrefix(spec.otelEndpoint, "/") {
		// Buildkit currently sets this to /dev/otel-grpc.sock which is not a valid
		// endpoint URL despite being set in an OTEL_* env var.
		spec.otelEndpoint = "unix://" + spec.otelEndpoint
	}

	if strings.HasPrefix(spec.otelEndpoint, "unix://") {
		// setup tcp proxying of unix endpoints for better client compatibility
		otelSockDestPath := strings.TrimPrefix(spec.otelEndpoint, "unix://")
		var otelSockSrcPath string
		var filteredMounts []specs.Mount
		for _, mnt := range spec.Mounts {
			if mnt.Destination == otelSockDestPath {
				otelSockSrcPath = mnt.Source
				continue
			}
			filteredMounts = append(filteredMounts, mnt)
		}
		if otelSockSrcPath == "" {
			return fmt.Errorf("no mount found for otel unix socket %s", otelSockDestPath)
		}
		spec.Mounts = filteredMounts

		listener, err := runInNamespace(ctx, spec.runState, func() (net.Listener, error) {
			return net.Listen("tcp", "127.0.0.1:0")
		})
		if err != nil {
			return fmt.Errorf("otel tcp proxy listen: %w", err)
		}
		spec.otelEndpoint = "http://" + listener.Addr().String()

		proxyConnPool := pool.New()
		listenerCtx, cancelListener := context.WithCancel(ctx)
		listenerPool := pool.New().WithContext(listenerCtx).WithCancelOnError()

		listenerPool.Go(func(ctx context.Context) error {
			<-ctx.Done()
			err := listener.Close()
			if err != nil {
				return fmt.Errorf("close otel proxy listener: %w", err)
			}
			return nil
		})
		listenerPool.Go(func(ctx context.Context) error {
			for {
				conn, err := listener.Accept()
				if err != nil {
					if errors.Is(err, net.ErrClosed) {
						err = nil
					}
					return err
				}

				// TODO:(sipsma) logging that existed before? Was it useful?

				remote, err := net.Dial("unix", otelSockSrcPath)
				if err != nil {
					conn.Close()
					return fmt.Errorf("dial otel unix socket: %w", err)
				}

				proxyConnPool.Go(func() {
					defer remote.Close()
					io.Copy(remote, conn)
				})

				proxyConnPool.Go(func() {
					defer conn.Close()
					io.Copy(conn, remote)
				})
			}
		})
		spec.cleanups.addNoErr("wait for otel proxy conn pool", proxyConnPool.Wait)
		spec.cleanups.add("wait for otel listener pool", listenerPool.Wait)
		spec.cleanups.addNoErr("cancel otel listener context", cancelListener)
	}

	spec.Process.Env = append(spec.Process.Env,
		OTELExporterProtocolEnv+"="+spec.otelProto,
		OTELExporterEndpointEnv+"="+spec.otelEndpoint,
		OTELTracesProtocolEnv+"="+spec.otelProto,
		OTELTracesEndpointEnv+"="+spec.otelEndpoint,
		// Dagger sets up a log exporter too. Explicitly set it so things can
		// detect support for it.
		OTELLogsProtocolEnv+"="+spec.otelProto,
		OTELLogsEndpointEnv+"="+spec.otelEndpoint,
		// Dagger doesn't set up metrics yet, but we should set this anyway,
		// since otherwise some tools default to localhost.
		OTELMetricsProtocolEnv+"="+spec.otelProto,
		OTELMetricsEndpointEnv+"="+spec.otelEndpoint,
	)

	return nil
}

func (w *Worker) setupSecretScrubbing(ctx context.Context, state *execState) error {
	if w.execMD == nil {
		return nil
	}
	if len(w.execMD.SecretEnvNames) == 0 && len(w.execMD.SecretFilePaths) == 0 {
		return nil
	}

	ctrCwd := spec.Process.Cwd
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
	stdoutScrubReader, err := NewSecretScrubReader(stdoutR, spec.Process.Env, w.execMD.SecretEnvNames, secretFilePaths)
	if err != nil {
		return fmt.Errorf("setup stdout secret scrubbing: %w", err)
	}
	stderrR, stderrW := io.Pipe()
	stderrScrubReader, err := NewSecretScrubReader(stderrR, spec.Process.Env, w.execMD.SecretEnvNames, secretFilePaths)
	if err != nil {
		return fmt.Errorf("setup stderr secret scrubbing: %w", err)
	}

	var pipeWg sync.WaitGroup

	finalStdout := spec.procInfo.Stdout
	spec.procInfo.Stdout = stdoutW
	pipeWg.Add(1)
	go func() {
		defer pipeWg.Done()
		io.Copy(finalStdout, stdoutScrubReader)
	}()

	finalStderr := spec.procInfo.Stderr
	spec.procInfo.Stderr = stderrW
	pipeWg.Add(1)
	go func() {
		defer pipeWg.Done()
		io.Copy(finalStderr, stderrScrubReader)
	}()

	spec.cleanups.add("close secret scrub stderr reader", stderrR.Close)
	spec.cleanups.add("close secret scrub stdout reader", stdoutR.Close)
	spec.cleanups.addNoErr("wait for secret scrubber pipes", pipeWg.Wait)
	spec.cleanups.add("close secret scrub stderr writer", stderrW.Close)
	spec.cleanups.add("close secret scrub stdout writer", stdoutW.Close)

	return nil
}

func (w *Worker) setProxyEnvs(_ context.Context, spec *spec) error {
	for _, upperProxyEnvName := range []string{
		"HTTP_PROXY",
		"HTTPS_PROXY",
		"FTP_PROXY",
		"NO_PROXY",
		"ALL_PROXY",
	} {
		upperProxyVal, upperSet := spec.origEnvMap[upperProxyEnvName]

		lowerProxyEnvName := strings.ToLower(upperProxyEnvName)
		lowerProxyVal, lowerSet := spec.origEnvMap[lowerProxyEnvName]

		// try to set both upper and lower case proxy env vars, some programs
		// only respect one or the other
		switch {
		case upperSet && lowerSet:
			// both were already set explicitly by the user, don't overwrite
			continue
		case upperSet:
			// upper case was set, set lower case to the same value
			spec.Process.Env = append(spec.Process.Env, lowerProxyEnvName+"="+upperProxyVal)
		case lowerSet:
			// lower case was set, set upper case to the same value
			spec.Process.Env = append(spec.Process.Env, upperProxyEnvName+"="+lowerProxyVal)
		default:
			// neither was set by the user, check if the engine itself has the upper case
			// set and pass that through to the container in both cases if so
			val, ok := os.LookupEnv(upperProxyEnvName)
			if ok {
				spec.Process.Env = append(spec.Process.Env, upperProxyEnvName+"="+val, lowerProxyEnvName+"="+val)
			}
		}
	}

	if w.execMD == nil {
		return nil
	}

	const systemEnvPrefix = "_DAGGER_ENGINE_SYSTEMENV_"
	for _, systemEnvName := range w.execMD.SystemEnvNames {
		if _, ok := spec.origEnvMap[systemEnvName]; ok {
			// don't overwrite explicit user-provided values
			continue
		}
		systemVal, ok := os.LookupEnv(systemEnvPrefix + systemEnvName)
		if ok {
			spec.Process.Env = append(spec.Process.Env, systemEnvName+"="+systemVal)
		}
	}

	return nil
}

func (w *Worker) enableGPU(_ context.Context, spec *spec) error {
	if w.execMD == nil {
		return nil
	}
	if len(w.execMD.EnabledGPUs) == 0 {
		return nil
	}

	if spec.Hooks == nil {
		spec.Hooks = &specs.Hooks{}
	}
	spec.Hooks.Prestart = append(spec.Hooks.Prestart, specs.Hook{
		Args: []string{
			"nvidia-container-runtime-hook",
			"prestart",
		},
		Path: "/usr/bin/nvidia-container-runtime-hook",
	})
	spec.Process.Env = append(spec.Process.Env, fmt.Sprintf("NVIDIA_VISIBLE_DEVICES=%s",
		strings.Join(w.execMD.EnabledGPUs, ","),
	))

	return nil
}

func (w *Worker) createCWD(_ context.Context, spec *spec) error {
	newp, err := fs.RootPath(spec.rootfsPath, spec.procInfo.Meta.Cwd)
	if err != nil {
		return fmt.Errorf("working dir %s points to invalid target: %w", newp, err)
	}
	if _, err := os.Stat(newp); err != nil {
		if err := idtools.MkdirAllAndChown(newp, 0o755, idtools.Identity{
			UID: int(spec.uid),
			GID: int(spec.gid),
		}); err != nil {
			return fmt.Errorf("failed to create working directory %s: %w", newp, err)
		}
	}

	return nil
}

func (w *Worker) installCACerts(ctx context.Context, spec *spec) error {
	if !spec.installCACerts {
		return nil
	}

	caInstaller, err := cacerts.NewInstaller(ctx, spec.Spec, func(ctx context.Context, args ...string) error {
		id := randid.NewID()
		meta := spec.procInfo.Meta

		// don't run this as a nested client, not necessary
		var filteredEnvs []string
		for _, env := range meta.Env {
			if strings.HasPrefix(env, "_DAGGER_NESTED_CLIENT_ID=") {
				continue
			}
			filteredEnvs = append(filteredEnvs, env)
		}
		meta.Env = filteredEnvs

		meta.Args = args
		meta.User = "0:0"
		meta.Cwd = "/"
		meta.Tty = false
		output := new(bytes.Buffer)
		process := executor.ProcessInfo{
			Stdout: nopCloser{output},
			Stderr: nopCloser{output},
			Meta:   meta,
		}
		started := make(chan struct{}, 1)
		err := w.run(
			ctx,
			id,
			spec.rootMount,
			spec.mounts,
			spec.rootfsPath,
			process,
			spec.networkNamespace,
			started,
			false,
		)
		if err != nil {
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
		spec.cleanups.add("uninstall CA certs", func() error {
			err := caInstaller.Uninstall(ctx)
			if err != nil {
				bklog.G(ctx).Errorf("failed to uninstall cacerts: %v", err)
			}
			return err
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

func (w *Worker) setupNestedClient(ctx context.Context, spec *spec) (rerr error) {
	if w.execMD == nil {
		return nil
	}
	if w.execMD.ClientID == "" {
		return nil
	}

	nestedClientMD := engine.ClientMetadata{
		ClientID:          w.execMD.ClientID,
		ClientSecretToken: randid.NewID(),
		ServerID:          w.execMD.ServerID,
		ClientHostname:    spec.Hostname,
		// TODO: Labels?
		// TODO: CloudToken?
		// TODO: DoNotTrack?
	}

	spec.Process.Env = append(spec.Process.Env, DaggerSessionTokenEnv+"="+nestedClientMD.ClientSecretToken)

	sessionCtx, cancelSession := context.WithCancel(ctx)
	defer func() {
		if rerr != nil {
			cancelSession()
		}
	}()

	bkSessionName := randid.NewID()
	bkSessionSharedKey := ""
	bkSession, err := bksession.NewSession(sessionCtx, bkSessionName, bkSessionSharedKey)
	if err != nil {
		return fmt.Errorf("create buildkit session: %w", err)
	}
	spec.cleanups.add("close nested client buildkit session", bkSession.Close)

	bkSession.Allow(client.SocketProvider{
		EnableHostNetworkAccess: true,
		Dialer: func(network, addr string) (net.Conn, error) {
			return runInNamespace(sessionCtx, spec.runState, func() (net.Conn, error) {
				return net.Dial(network, addr)
			})
		},
	})
	bkSession.Allow(session.NewTunnelListenerAttachable(sessionCtx, func(network, addr string) (net.Listener, error) {
		return runInNamespace(sessionCtx, spec.runState, func() (net.Listener, error) {
			return net.Listen(network, addr)
		})
	}))

	// TODO: do this much earlier for all containers, will enable cleaning up containerfs code massively
	// TODO: do this much earlier for all containers, will enable cleaning up containerfs code massively
	// TODO: do this much earlier for all containers, will enable cleaning up containerfs code massively
	var bindMnts []mount.Mount
	var filteredMounts []specs.Mount
	for _, mnt := range spec.Mounts {
		switch mnt.Type {
		case "overlay":
			// should never happen, we should only be given bind mounts from overlays
			return fmt.Errorf("unexpected overlay mount in spec: %v", mnt)
		case "bind", "rbind": // not an actual mount type in linux, but containerd/buildkit code sets this anyways
			bindMnts = append(bindMnts, mount.Mount{
				Type:    mnt.Type,
				Source:  mnt.Source,
				Target:  mnt.Destination,
				Options: mnt.Options,
			})
		default:
			filteredMounts = append(filteredMounts, mnt)
		}
	}
	spec.Mounts = filteredMounts

	for _, bindMnt := range bindMnts {
		dstPath, err := fs.RootPath(spec.rootfsPath, bindMnt.Target)
		if err != nil {
			return fmt.Errorf("bind mount %s points to invalid target: %w", bindMnt.Target, err)
		}
		// ref: https://github.com/opencontainers/runc/blob/9d02c20df7faf7b356a632e35dfccf332fc7efed/libcontainer/rootfs_linux.go#L1173
		if _, err := os.Stat(dstPath); err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("stat bind mount target %s: %w", dstPath, err)
			}
			srcStat, err := os.Stat(bindMnt.Source)
			if err != nil {
				return fmt.Errorf("stat bind mount source %s: %w", bindMnt.Source, err)
			}
			switch srcStat.Mode() & os.ModeType {
			case os.ModeDir:
				if err := os.MkdirAll(dstPath, 0o755); err != nil {
					return fmt.Errorf("create bind mount target dir %s: %w", dstPath, err)
				}
			default:
				if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
					return fmt.Errorf("create bind mount target parent dir %s: %w", dstPath, err)
				}
				if f, err := os.OpenFile(dstPath, os.O_CREATE, 0o755); err != nil {
					return fmt.Errorf("create bind mount target file %s: %w", dstPath, err)
				} else {
					f.Close()
				}
			}
		}
	}

	if err := mount.All(bindMnts, spec.rootfsPath); err != nil {
		return fmt.Errorf("mount bind mounts: %w", err)
	}
	spec.cleanups.add("unmount container bind mounts", func() error {
		return mount.UnmountMounts(bindMnts, spec.rootfsPath, 0)
	})

	// TODO: HANDLE CWD NOT EXISTING? OR I THINK THATS ALREADY DONE SOMEHWERE?
	cwdPath := spec.rootfsPath
	if spec.Process.Cwd != "" {
		cwdPath, err = fs.RootPath(spec.rootfsPath, spec.Process.Cwd)
		if err != nil {
			return fmt.Errorf("working dir %s points to invalid target: %w", spec.Process.Cwd, err)
		}
	}

	// TODO: eval symlinks on rootfsPath just to be extra safe?
	filesyncer, err := client.NewFilesyncer(spec.rootfsPath, cwdPath, &spec.uid, &spec.gid)
	if err != nil {
		return fmt.Errorf("create filesyncer: %w", err)
	}
	bkSession.Allow(filesyncer.AsSource())
	bkSession.Allow(filesyncer.AsTarget())

	// TODO: auth provider? or nah?

	buildkitSessionMDCh := make(chan map[string][]string)
	sessionClientConn, sessionServerConn := net.Pipe()
	sessionPool := pool.New().WithContext(sessionCtx).WithCancelOnError()
	sessionPool.Go(func(ctx context.Context) error {
		defer close(buildkitSessionMDCh)
		return bkSession.Run(ctx, func(ctx context.Context, proto string, meta map[string][]string) (net.Conn, error) {
			select {
			case <-ctx.Done():
				return nil, context.Cause(ctx)
			case buildkitSessionMDCh <- meta:
			}
			return sessionClientConn, nil
		})
	})

	var bkSessionMD map[string][]string
	select {
	case <-sessionCtx.Done():
		return fmt.Errorf("session closed before buildkit session metadata received: %w", context.Cause(sessionCtx))
	case bkSessionMD = <-buildkitSessionMDCh:
	}

	registerClientMD := nestedClientMD
	registerClientMD.RegisterClient = true
	sessionPool.Go(func(ctx context.Context) error {
		defer sessionClientConn.Close()
		ctx = engine.ContextWithClientMetadata(ctx, &registerClientMD)
		return w.Controller.HandleConn(ctx, sessionServerConn, &registerClientMD, bkSessionMD)
	})

	httpListener, err := runInNamespace(ctx, spec.runState, func() (net.Listener, error) {
		return net.Listen("tcp", "127.0.0.1:0")
	})
	if err != nil {
		return fmt.Errorf("listen for nested client: %w", err)
	}

	tcpAddr, ok := httpListener.Addr().(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("unexpected listener address type: %T", httpListener.Addr())
	}
	spec.Process.Env = append(spec.Process.Env, DaggerSessionPortEnv+"="+strconv.Itoa(tcpAddr.Port))

	handleConnPool := pool.New().WithContext(sessionCtx)
	httpListenerPool := pool.New().WithContext(sessionCtx).WithCancelOnError()
	httpListenerPool.Go(func(ctx context.Context) error {
		<-ctx.Done()
		err := httpListener.Close()
		if err != nil {
			return fmt.Errorf("close nested client listener: %w", err)
		}
		return nil
	})
	httpListenerPool.Go(func(ctx context.Context) error {
		for {
			conn, err := httpListener.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return nil
				}
				return fmt.Errorf("accept nested client connection: %w", err)
			}

			handleConnPool.Go(func(ctx context.Context) error {
				defer conn.Close()
				ctx = engine.ContextWithClientMetadata(ctx, &nestedClientMD)
				return w.Controller.HandleConn(ctx, conn, &nestedClientMD, nil)
			})
		}
	})

	spec.cleanups.add("wait for nested client buildkit session pool", sessionPool.Wait)
	spec.cleanups.add("wait for nested client conn pool", handleConnPool.Wait)
	spec.cleanups.add("wait for nested client listener pool", httpListenerPool.Wait)
	spec.cleanups.addNoErr("cancel nested client buildkit session", cancelSession)

	return nil
}
