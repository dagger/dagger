package buildkit

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"

	ctdoci "github.com/containerd/containerd/oci"
	"github.com/moby/buildkit/executor"
	"github.com/moby/buildkit/executor/oci"
	"github.com/moby/buildkit/util/network"
	"github.com/opencontainers/runtime-spec/specs-go"

	"github.com/dagger/dagger/engine/buildkit/containerfs"
)

const (
	DaggerServerIDEnv       = "_DAGGER_SERVER_ID"
	DaggerClientIDEnv       = "_DAGGER_NESTED_CLIENT_ID"
	DaggerCallDigestEnv     = "_DAGGER_CALL_DIGEST"
	DaggerEngineVersionEnv  = "_DAGGER_ENGINE_VERSION"
	DaggerRedirectStdoutEnv = "_DAGGER_REDIRECT_STDOUT"
	DaggerRedirectStderrEnv = "_DAGGER_REDIRECT_STDERR"
)

// some envs that are used to scope cache but not needed at runtime
var removeEnvs = map[string]struct{}{
	DaggerCallDigestEnv:     {},
	DaggerEngineVersionEnv:  {},
	DaggerRedirectStdoutEnv: {},
	DaggerRedirectStderrEnv: {},
}

type spec struct {
	// should be set by the caller
	cleanups   *cleanups
	procInfo   *executor.ProcessInfo
	rootfsPath string
	rootMount  executor.Mount
	mounts     []executor.Mount
	id         string
	resolvConf string
	hostsFile  string
	namespace  network.Namespace
	extraOpts  []ctdoci.SpecOpts

	// will be set by the generator
	*specs.Spec
	exitCodePath string
	metaMount    *specs.Mount
	origEnvMap   map[string]string
}

type specFunc func(context.Context, *spec) error

func (w *Worker) generateBaseSpec(ctx context.Context, spec *spec) error {
	baseSpec, ociSpecCleanup, err := oci.GenerateSpec(
		ctx,
		spec.procInfo.Meta,
		spec.mounts,
		spec.id,
		spec.resolvConf,
		spec.hostsFile,
		spec.namespace,
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
	spec.cleanups.addNoErr(ociSpecCleanup)

	spec.Spec = baseSpec
	return nil
}

func (w *Worker) setOrigEnvMap(_ context.Context, spec *spec) error {
	spec.origEnvMap = make(map[string]string)
	for _, env := range spec.Process.Env {
		k, v, ok := strings.Cut(env, "=")
		if !ok {
			continue
		}
		spec.origEnvMap[k] = v
	}
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

	// TODO: remove this once shim is fully gone
	// TODO: remove this once shim is fully gone
	// TODO: remove this once shim is fully gone
	_, isDaggerExec := spec.origEnvMap["_DAGGER_INTERNAL_COMMAND"]
	isDaggerExec = isDaggerExec || spec.metaMount != nil
	if isDaggerExec {
		shimBinPath := w.runc.Command
		if !filepath.IsAbs(shimBinPath) {
			var err error
			shimBinPath, err = exec.LookPath(shimBinPath)
			if err != nil {
				return fmt.Errorf("find shim binary: %w", err)
			}
		}
		shimBinPath, err := filepath.EvalSymlinks(shimBinPath)
		if err != nil {
			return fmt.Errorf("resolve shim binary symlink: %w", err)
		}

		spec.Mounts = append(spec.Mounts, specs.Mount{
			Destination: "/.shim",
			Type:        "bind",
			Source:      shimBinPath,
			Options:     []string{"rbind", "ro"},
		})
		spec.Process.Args = append([]string{"/.shim"}, spec.Process.Args...)
	}

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
		spec.cleanups.add(stdinFile.Close)
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
	spec.cleanups.add(stdoutFile.Close)
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
	spec.cleanups.add(stderrFile.Close)
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
			spec.cleanups.add(redirectStdoutFile.Close)
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
			spec.cleanups.add(redirectStderrFile.Close)
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

// TODO: OTEL IS NOT GOING THROUGH THIS RIGHT BUT TESTS STILL PASS BECAUSE THEY USE STDOUT/ERR API
// TODO: MAKE SURE THAT OTEL IS SCRUBBED BY MANUALLY RUNNING SECRET INTEG TESTS
// TODO: MAKE SURE THAT OTEL IS SCRUBBED BY MANUALLY RUNNING SECRET INTEG TESTS
// TODO: MAKE SURE THAT OTEL IS SCRUBBED BY MANUALLY RUNNING SECRET INTEG TESTS
// TODO: MAKE SURE THAT OTEL IS SCRUBBED BY MANUALLY RUNNING SECRET INTEG TESTS
// TODO: MAKE SURE THAT OTEL IS SCRUBBED BY MANUALLY RUNNING SECRET INTEG TESTS
// TODO: MAKE SURE THAT OTEL IS SCRUBBED BY MANUALLY RUNNING SECRET INTEG TESTS
// TODO: MAKE SURE THAT OTEL IS SCRUBBED BY MANUALLY RUNNING SECRET INTEG TESTS
// TODO: MAKE SURE THAT OTEL IS SCRUBBED BY MANUALLY RUNNING SECRET INTEG TESTS
// TODO: MAKE SURE THAT OTEL IS SCRUBBED BY MANUALLY RUNNING SECRET INTEG TESTS
func (w *Worker) setupSecretScrubbing(_ context.Context, spec *spec) error {
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
		for i := len(spec.Mounts) - 1; i >= 0; i-- {
			mnt := spec.Mounts[i]
			if mnt.Destination == filePath {
				secretFilePaths = append(secretFilePaths, mnt.Source)
				break
			}
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

	spec.cleanups.add(stderrR.Close)
	spec.cleanups.add(stdoutR.Close)
	spec.cleanups.addNoErr(pipeWg.Wait)
	spec.cleanups.add(stderrW.Close)
	spec.cleanups.add(stdoutW.Close)

	return nil
}

func (w *Worker) setProxyEnvs(_ context.Context, spec *spec) error {
	filteredEnvs := make([]string, 0, len(spec.Process.Env))
	for _, env := range spec.Process.Env {
		k, _, ok := strings.Cut(env, "=")
		if !ok {
			continue
		}
		if _, ok := removeEnvs[k]; ok {
			continue
		}
		filteredEnvs = append(filteredEnvs, env)
	}
	spec.Process.Env = filteredEnvs

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

func (w *Worker) setupOTEL(_ context.Context, spec *spec) error {
	if w.execMD == nil {
		return nil
	}
	spec.Process.Env = append(spec.Process.Env, w.execMD.OTELEnvs...)

	return nil
}

func (w *Worker) setupNestedClient(_ context.Context, spec *spec) error {
	// TODO: don't do basically any of this anymore once we serve nested clients from here
	// TODO: don't do basically any of this anymore once we serve nested clients from here
	// TODO: don't do basically any of this anymore once we serve nested clients from here
	if w.execMD == nil {
		return nil
	}
	spec.Process.Env = append(spec.Process.Env, DaggerServerIDEnv+"="+w.execMD.ServerID)

	if w.execMD.ClientID == "" {
		// don't let users manually set these
		for _, envName := range []string{
			DaggerServerIDEnv,
			DaggerClientIDEnv,
		} {
			if _, ok := spec.origEnvMap[envName]; ok {
				return fmt.Errorf("cannot set %s manually", envName)
			}
		}
		return nil
	}

	spec.Process.Env = append(spec.Process.Env, DaggerClientIDEnv+"="+w.execMD.ClientID)
	spec.Mounts = append(spec.Mounts, specs.Mount{
		Destination: "/.runner.sock",
		Type:        "bind",
		Options:     []string{"rbind"},
		Source:      "/run/buildkit/buildkitd.sock",
	})

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
