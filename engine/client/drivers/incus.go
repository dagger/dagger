package drivers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/xdg"
	"github.com/dagger/dagger/engine/client/imageload"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/dagger/util/traceexec"
	telemetry "github.com/dagger/otel-go"
	"github.com/docker/cli/cli/connhelper/commandconn"
)

type incus struct{}

var _ containerBackend = incus{}

const incusDockerRemote = "docker"
const incusDockerRemoteAddr = "https://docker.io"
const incusDockerRemoteProtocol = "oci"

var incusHostStateDir = filepath.Join(xdg.DataHome, "dagger", "incus")

type incusRemote struct {
	Addr     string `json:"addr"`
	Protocol string `json:"protocol"`
}

func (incus) Available(ctx context.Context) (bool, error) {
	if _, err := exec.LookPath("incus"); err != nil {
		return false, nil //nolint:nilerr
	}
	cmd := exec.CommandContext(ctx, "incus", "info")
	_, stderr, err := traceexec.ExecOutput(ctx, cmd, telemetry.Encapsulated())
	if err != nil {
		if isIncusDaemonUnavailableOutput(stderr) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (incus) ImagePull(ctx context.Context, image string) error {
	source, needsDockerRemote := incusRemoteImageRef(image)
	if needsDockerRemote {
		if err := ensureIncusDockerRemote(ctx); err != nil {
			return err
		}
	}
	alias := incusImageAlias(image)
	exists, err := incusImageExists(ctx, alias)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	args := []string{"image", "copy", source, "local:", "--alias", alias}
	return traceexec.Exec(ctx, exec.CommandContext(ctx, "incus", args...), telemetry.Encapsulated())
}

func (incus) ImageExists(ctx context.Context, image string) (bool, error) {
	return incusImageExists(ctx, incusImageAlias(image))
}

func (incus) ImageRemove(ctx context.Context, image string) error {
	return traceexec.Exec(ctx, exec.CommandContext(ctx, "incus", "image", "delete", "local:"+incusImageAlias(image)))
}

func (incus) ImageLoader(ctx context.Context) imageload.Backend {
	return imageload.Incus{}
}

func (incus) ContainerRun(ctx context.Context, name string, opts runOpts) error {
	if opts.gpus {
		return fmt.Errorf("incus backend does not currently support GPU passthrough")
	}
	if err := os.MkdirAll(incusHostStateDir, 0o755); err != nil {
		return err
	}

	alias := incusImageAlias(opts.image)
	exists, err := incusImageExists(ctx, alias)
	if err != nil {
		return err
	}
	if !exists {
		if err := (incus{}).ImagePull(ctx, opts.image); err != nil {
			return err
		}
	}
	stateDir, err := incusStateVolumeDir(name)
	if err != nil {
		return err
	}
	cfgDir, hasConfigDir, err := incusConfigDir()
	if err != nil {
		return err
	}
	args, err := incusLaunchArgs(name, opts, stateDir, cfgDir, hasConfigDir)
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, "incus", args...)
	_, stderr, err := traceexec.ExecOutput(ctx, cmd, telemetry.Encapsulated())
	if err != nil {
		if isIncusAlreadyExistsOutput(stderr) {
			return errContainerAlreadyExists
		}
		return err
	}
	return nil
}

func (incus) ContainerExec(ctx context.Context, name string, args []string) (string, string, error) {
	cmdArgs := append([]string{"exec", "-T", name, "--"}, args...)
	return traceexec.ExecOutput(ctx, exec.CommandContext(ctx, "incus", cmdArgs...))
}

func (incus) ContainerDial(ctx context.Context, name string, args []string) (net.Conn, error) {
	cmdArgs := append([]string{"exec", "-T", name, "--"}, args...)
	return commandconn.New(ctx, "incus", cmdArgs...)
}

func (incus) ContainerRemove(ctx context.Context, name string) error {
	return traceexec.Exec(ctx, exec.CommandContext(ctx, "incus", "delete", "-f", name))
}

func (i incus) ContainerStart(ctx context.Context, name string) error {
	running, err := i.containerIsRunning(ctx, name)
	if err != nil {
		return err
	}
	if running {
		return nil
	}
	return traceexec.Exec(ctx, exec.CommandContext(ctx, "incus", "start", name), telemetry.Encapsulated())
}

func (incus) ContainerExists(ctx context.Context, name string) (bool, error) {
	_, stderr, err := traceexec.ExecOutput(ctx, exec.CommandContext(ctx, "incus", "info", name), telemetry.Encapsulated())
	if err == nil {
		return true, nil
	}
	if isIncusNotFoundOutput(stderr) {
		return false, nil
	}
	return false, err
}

func (incus) ContainerLs(ctx context.Context) ([]string, error) {
	stdout, stderr, err := traceexec.ExecOutput(ctx, exec.CommandContext(ctx, "incus", "list", "--all", "--format", "json"))
	if err != nil && strings.Contains(stderr, "unknown flag: --all") {
		stdout, _, err = traceexec.ExecOutput(ctx, exec.CommandContext(ctx, "incus", "list", "--format", "json"))
	}
	if err != nil {
		return nil, err
	}
	var result []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(result))
	for _, res := range result {
		if res.Name != "" {
			ids = append(ids, res.Name)
		}
	}
	return ids, nil
}

func (incus) ContainerReady(ctx context.Context, name string, opts runOpts) error {
	probe := []string{"sh", "-ec", readinessProbeCommand(opts)}
	readyCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var lastErr error
	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()

	for range 80 {
		_, _, err := traceexec.ExecOutput(readyCtx, exec.CommandContext(readyCtx, "incus", append([]string{"exec", "-T", name, "--"}, probe...)...))
		if err == nil {
			return nil
		}
		lastErr = err

		select {
		case <-readyCtx.Done():
			return fmt.Errorf("timed out waiting for engine container %q to become ready: %w", name, lastErr)
		case <-ticker.C:
		}
	}

	return fmt.Errorf("timed out waiting for engine container %q to become ready: %w", name, lastErr)
}

func (i incus) containerIsRunning(ctx context.Context, name string) (bool, error) {
	stdout, _, err := traceexec.ExecOutput(ctx, exec.CommandContext(ctx, "incus", "list", name, "--format", "json"))
	if err != nil {
		return false, err
	}
	var result []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		return false, err
	}
	for _, res := range result {
		if res.Name == name {
			return strings.EqualFold(res.Status, "running"), nil
		}
	}
	return false, nil
}

func incusImageAlias(image string) string {
	sum := sha256.Sum256([]byte(image))
	return "dagger-" + hex.EncodeToString(sum[:8])
}

func readinessProbeCommand(opts runOpts) string {
	addr := distconsts.DefaultEngineSockAddr
	if opts.port != 0 {
		addr = fmt.Sprintf("tcp://127.0.0.1:%d", opts.port)
	}

	return fmt.Sprintf(`if command -v buildctl >/dev/null 2>&1; then
		buildctl --addr %s debug workers >/dev/null 2>&1
	else
		test -S /run/dagger/engine.sock
	fi`, shellQuote(addr))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func incusLaunchArgs(name string, opts runOpts, stateDir, cfgDir string, hasConfigDir bool) ([]string, error) {
	if opts.gpus {
		return nil, fmt.Errorf("incus backend does not currently support GPU passthrough")
	}

	args := []string{"launch", "local:" + incusImageAlias(opts.image), name}
	args = append(args, "-c", "security.nesting=true")
	if opts.privileged {
		args = append(args, "-c", "security.privileged=true")
	}
	if opts.cpus != "" {
		args = append(args, "-c", "limits.cpu="+opts.cpus)
	}
	if opts.memory != "" {
		args = append(args, "-c", "limits.memory="+opts.memory)
	}

	args = append(args, "-d", "dagger-state,type=disk,source="+stateDir+",path="+distconsts.EngineDefaultStateDir)
	if hasConfigDir {
		args = append(args, "-d", "dagger-config,type=disk,source="+cfgDir+",path="+filepath.Join("/root", ".config", "dagger"))
	}

	for _, env := range opts.env {
		k, v, ok := strings.Cut(env, "=")
		if !ok {
			v = ""
		}
		args = append(args, "-c", "environment."+k+"="+v)
	}

	for _, port := range opts.ports {
		hostPort, containerPort, ok := strings.Cut(port, ":")
		if !ok {
			hostPort = port
			containerPort = port
		}
		args = append(args, "-d", "dagger-port-"+hostPort+",type=proxy,listen=tcp:127.0.0.1:"+hostPort+",connect=tcp:127.0.0.1:"+containerPort)
	}

	args = append(args, "--")
	args = append(args, opts.args...)
	return args, nil
}

func incusRemoteImageRef(image string) (string, bool) {
	if strings.Contains(image, "://") || strings.HasPrefix(image, "local:") || strings.HasPrefix(image, "docker:") || strings.HasPrefix(image, "images:") {
		return image, false
	}
	return "docker:" + image, true
}

func incusImageExists(ctx context.Context, alias string) (bool, error) {
	_, stderr, err := traceexec.ExecOutput(ctx, exec.CommandContext(ctx, "incus", "image", "info", "local:"+alias), telemetry.Encapsulated())
	if err == nil {
		return true, nil
	}
	if isIncusNotFoundOutput(stderr) {
		return false, nil
	}
	return false, err
}

func ensureIncusDockerRemote(ctx context.Context) error {
	remotes, err := incusRemoteList(ctx)
	if err == nil {
		if remote, ok := remotes[incusDockerRemote]; ok {
			if isExpectedIncusDockerRemote(remote) {
				return nil
			}
			return fmt.Errorf("incus remote %q already exists but with different configuration: protocol=%q addr=%q", incusDockerRemote, remote.Protocol, remote.Addr)
		}
	}

	cmd := exec.CommandContext(ctx, "incus", "remote", "add", incusDockerRemote, incusDockerRemoteAddr, "--protocol="+incusDockerRemoteProtocol)
	_, stderr, err := traceexec.ExecOutput(ctx, cmd, telemetry.Encapsulated())
	if err != nil {
		if !strings.Contains(strings.ToLower(stderr), "already exists") {
			return err
		}
		remotes, err := incusRemoteList(ctx)
		if err != nil {
			return err
		}
		if remote, ok := remotes[incusDockerRemote]; ok {
			if isExpectedIncusDockerRemote(remote) {
				return nil
			}
			return fmt.Errorf("incus remote %q already exists but with different configuration: protocol=%q addr=%q", incusDockerRemote, remote.Protocol, remote.Addr)
		}
		return fmt.Errorf("incus remote %q already exists but could not be verified", incusDockerRemote)
	}
	return nil
}

func isExpectedIncusDockerRemote(remote incusRemote) bool {
	return strings.EqualFold(remote.Protocol, incusDockerRemoteProtocol) &&
		(remote.Addr == incusDockerRemoteAddr || remote.Addr == "docker.io")
}

func incusConfigDir() (string, bool, error) {
	dir := filepath.Join(xdg.ConfigHome, "dagger")
	if _, err := os.Stat(dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	return dir, true, nil
}

func incusStateVolumeDir(name string) (string, error) {
	dir := filepath.Join(incusHostStateDir, "volumes", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func isIncusAlreadyExistsOutput(output string) bool {
	output = strings.ToLower(output)
	return strings.Contains(output, "already exists") || strings.Contains(output, "instance already exists")
}

func isIncusNotFoundOutput(output string) bool {
	output = strings.ToLower(output)
	return strings.Contains(output, "not found") || strings.Contains(output, "not found in project")
}

func isIncusDaemonUnavailableOutput(output string) bool {
	output = strings.ToLower(output)
	return strings.Contains(output, "unable to connect") ||
		strings.Contains(output, "connection refused") ||
		strings.Contains(output, "apiserver is not running") ||
		strings.Contains(output, "not registered with launchd") ||
		strings.Contains(output, "permission denied") ||
		strings.Contains(output, "the server isn't available")
}

func incusRemoteList(ctx context.Context) (map[string]incusRemote, error) {
	stdout, _, err := traceexec.ExecOutput(ctx, exec.CommandContext(ctx, "incus", "remote", "list", "--format", "json"))
	if err != nil {
		return nil, err
	}
	var remotes map[string]incusRemote
	if err := json.Unmarshal([]byte(stdout), &remotes); err != nil {
		return nil, err
	}
	return remotes, nil
}
