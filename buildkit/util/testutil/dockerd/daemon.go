package dockerd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/moby/buildkit/identity"
	"github.com/pkg/errors"
)

type LogT interface {
	Logf(string, ...interface{})
}

type nopLog struct{}

func (nopLog) Logf(string, ...interface{}) {}

const (
	shortLen             = 12
	DefaultDockerdBinary = "dockerd"
)

type Option func(*Daemon)

type Daemon struct {
	root          string
	folder        string
	Wait          chan error
	id            string
	cmd           *exec.Cmd
	storageDriver string
	execRoot      string
	dockerdBinary string
	Log           LogT
	pidFile       string
	sockPath      string
	args          []string
	envs          []string
}

var sockRoot = filepath.Join(os.TempDir(), "docker-integration")

func NewDaemon(workingDir string, ops ...Option) (*Daemon, error) {
	if err := os.MkdirAll(sockRoot, 0700); err != nil {
		return nil, errors.Wrapf(err, "failed to create daemon socket root %q", sockRoot)
	}

	id := "d" + identity.NewID()[:shortLen]
	daemonFolder, err := filepath.Abs(filepath.Join(workingDir, id))
	if err != nil {
		return nil, err
	}
	daemonRoot := filepath.Join(daemonFolder, "root")
	if err := os.MkdirAll(daemonRoot, 0755); err != nil {
		return nil, errors.Wrapf(err, "failed to create daemon root %q", daemonRoot)
	}

	d := &Daemon{
		id:            id,
		folder:        daemonFolder,
		root:          daemonRoot,
		storageDriver: os.Getenv("DOCKER_GRAPHDRIVER"),
		// dxr stands for docker-execroot (shortened for avoiding unix(7) path length limitation)
		execRoot:      filepath.Join(os.TempDir(), "dxr", id),
		dockerdBinary: DefaultDockerdBinary,
		Log:           nopLog{},
		sockPath:      filepath.Join(sockRoot, id+".sock"),
		envs:          append([]string{}, os.Environ()...),
	}

	for _, op := range ops {
		op(d)
	}

	return d, nil
}

func WithBinary(bin string) Option {
	return func(d *Daemon) {
		d.dockerdBinary = bin
	}
}

func WithExtraEnv(envs []string) Option {
	return func(d *Daemon) {
		d.envs = append(d.envs, envs...)
	}
}

func (d *Daemon) Sock() string {
	return "unix://" + d.sockPath
}

func (d *Daemon) StartWithError(daemonLogs map[string]*bytes.Buffer, providedArgs ...string) error {
	dockerdBinary, err := exec.LookPath(d.dockerdBinary)
	if err != nil {
		return errors.Wrapf(err, "[%s] could not find dockerd binary %q in $PATH", d.id, d.dockerdBinary)
	}

	if d.pidFile == "" {
		d.pidFile = filepath.Join(d.folder, "docker.pid")
	}

	d.args = []string{
		"--data-root", d.root,
		"--exec-root", d.execRoot,
		"--pidfile", d.pidFile,
		"--containerd-namespace", d.id,
		"--containerd-plugins-namespace", d.id + "p",
		"--host", d.Sock(),
	}
	if root := os.Getenv("DOCKER_REMAP_ROOT"); root != "" {
		d.args = append(d.args, "--userns-remap", root)
	}

	// If we don't explicitly set the log-level or debug flag(-D) then
	// turn on debug mode
	var foundLog, foundSd bool
	for _, a := range providedArgs {
		if strings.Contains(a, "--log-level") || strings.Contains(a, "-D") || strings.Contains(a, "--debug") {
			foundLog = true
		}
		if strings.Contains(a, "--storage-driver") {
			foundSd = true
		}
	}
	if !foundLog {
		d.args = append(d.args, "--debug")
	}
	if d.storageDriver != "" && !foundSd {
		d.args = append(d.args, "--storage-driver", d.storageDriver)
	}

	d.args = append(d.args, providedArgs...)
	d.cmd = exec.Command(dockerdBinary, d.args...)
	d.cmd.Env = append(d.envs, "DOCKER_SERVICE_PREFER_OFFLINE_IMAGE=1", "BUILDKIT_DEBUG_EXEC_OUTPUT=1", "BUILDKIT_DEBUG_PANIC_ON_ERROR=1")

	if daemonLogs != nil {
		b := new(bytes.Buffer)
		daemonLogs["stdout: "+d.cmd.Path] = b
		d.cmd.Stdout = &lockingWriter{Writer: b}
		b = new(bytes.Buffer)
		daemonLogs["stderr: "+d.cmd.Path] = b
		d.cmd.Stderr = &lockingWriter{Writer: b}
	}

	fmt.Fprintf(d.cmd.Stderr, "> startCmd %v %+v\n", time.Now(), d.cmd.String())
	if err := d.cmd.Start(); err != nil {
		return errors.Wrapf(err, "[%s] could not start daemon container", d.id)
	}

	wait := make(chan error, 1)

	go func() {
		ret := d.cmd.Wait()
		d.Log.Logf("[%s] exiting daemon", d.id)
		// If we send before logging, we might accidentally log _after_ the test is done.
		// As of Go 1.12, this incurs a panic instead of silently being dropped.
		wait <- ret
		close(wait)
	}()

	d.Wait = wait

	d.Log.Logf("[%s] daemon started\n", d.id)
	return nil
}

var errDaemonNotStarted = errors.New("daemon not started")

func (d *Daemon) StopWithError() (err error) {
	if d.cmd == nil || d.Wait == nil {
		return errDaemonNotStarted
	}
	defer func() {
		if err != nil {
			d.Log.Logf("[%s] error while stopping daemon: %v", d.id, err)
		} else {
			d.Log.Logf("[%s] daemon stopped", d.id)
			if d.pidFile != "" {
				_ = os.Remove(d.pidFile)
			}
		}
		d.cmd = nil
	}()

	i := 1
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	tick := ticker.C

	d.Log.Logf("[%s] stopping daemon", d.id)

	if err := d.cmd.Process.Signal(os.Interrupt); err != nil {
		if strings.Contains(err.Error(), "os: process already finished") {
			return errDaemonNotStarted
		}
		return errors.Wrapf(err, "[%s] could not send signal", d.id)
	}

out1:
	for {
		select {
		case err := <-d.Wait:
			return err
		case <-time.After(20 * time.Second):
			// time for stopping jobs and run onShutdown hooks
			d.Log.Logf("[%s] daemon stop timed out after 20 seconds", d.id)
			break out1
		}
	}

out2:
	for {
		select {
		case err := <-d.Wait:
			return err
		case <-tick:
			i++
			if i > 5 {
				d.Log.Logf("[%s] tried to interrupt daemon for %d times, now try to kill it", d.id, i)
				break out2
			}
			d.Log.Logf("[%d] attempt #%d/5: daemon is still running with pid %d", i, d.cmd.Process.Pid)
			if err := d.cmd.Process.Signal(os.Interrupt); err != nil {
				return errors.Wrapf(err, "[%s] attempt #%d/5 could not send signal", d.id, i)
			}
		}
	}

	if err := d.cmd.Process.Kill(); err != nil {
		d.Log.Logf("[%s] failed to kill daemon: %v", d.id, err)
		return err
	}

	return nil
}

type lockingWriter struct {
	mu sync.Mutex
	io.Writer
}

func (w *lockingWriter) Write(dt []byte) (int, error) {
	w.mu.Lock()
	n, err := w.Writer.Write(dt)
	w.mu.Unlock()
	return n, err
}
