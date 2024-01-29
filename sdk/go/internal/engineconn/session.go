package engineconn

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

type cliSessionConn struct {
	*http.Client
	childCancel func()
	childProc   *exec.Cmd
}

func (c *cliSessionConn) Host() string {
	return "dagger"
}

func (c *cliSessionConn) Close() error {
	if c.childCancel != nil && c.childProc != nil {
		c.childCancel()
		err := c.childProc.Wait()
		if err != nil {
			if errors.Is(err, context.Canceled) {
				// expected
				return nil
			}

			return err
		}
	}
	return nil
}

func getSDKVersion() string {
	version := "n/a"
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return version
	}

	for _, dep := range info.Deps {
		if dep.Path == "dagger.io/dagger" {
			version = dep.Version
			if version[0] == 'v' {
				version = version[1:]
			}
			break
		}
	}
	return version
}

func startCLISession(ctx context.Context, binPath string, cfg *Config) (_ EngineConn, rerr error) {
	args := []string{"session"}

	version := getSDKVersion()

	flagsAndValues := []struct {
		flag  string
		value string
	}{
		{"--workdir", cfg.Workdir},
		{"--label", "dagger.io/sdk.name:go"},
		{"--label", fmt.Sprintf("dagger.io/sdk.version:%s", version)},
	}

	for _, pair := range flagsAndValues {
		if pair.value != "" {
			args = append(args, pair.flag, pair.value)
		}
	}

	env := os.Environ()

	cmdCtx, cmdCancel := context.WithCancel(ctx)

	// Workaround https://github.com/golang/go/issues/22315
	// Basically, if any other code in this process does fork/exec, it may
	// temporarily have the tmpbin fd that we closed earlier open still, and it
	// will be open for writing. Even though we rename the file, the
	// underlying inode is the same and thus we can get a "text file busy"
	// error when trying to exec it below.
	//
	// We workaround this the same way suggested in the issue, by sleeping
	// and retrying the exec a few times. This is such an obscure case that
	// this retry approach should be fine. It can only happen when a new
	// dagger binary needs to be created and even then only if many
	// threads within this process are trying to provision it at the same time.
	var proc *exec.Cmd
	var stdout io.ReadCloser
	var stderrBuf *safeBuffer
	var childStdin io.WriteCloser

	if cfg.LogOutput != nil {
		fmt.Fprintf(cfg.LogOutput, "Creating new Engine session... ")
	}

	for i := 0; i < 10; i++ {
		proc = exec.CommandContext(cmdCtx, binPath, args...)
		proc.Env = env

		var err error
		stdout, err = proc.StdoutPipe()
		if err != nil {
			cmdCancel()
			return nil, err
		}
		defer stdout.Close() // don't need it after we read the port

		stderrPipe, err := proc.StderrPipe()
		if err != nil {
			cmdCancel()
			return nil, err
		}
		if cfg.LogOutput == nil {
			cfg.LogOutput = io.Discard
		}

		// Write stderr to logWriter but also buffer it for the duration
		// of this function so we can return it in the error if something
		// goes wrong here. Otherwise the only error ends up being EOF and
		// the user has to enable log output to see anything.
		stderrBuf = &safeBuffer{}
		discardableBuf := &discardableWriter{w: stderrBuf}
		go io.Copy(io.MultiWriter(cfg.LogOutput, discardableBuf), stderrPipe)
		defer discardableBuf.Discard()

		// Open a stdin pipe with the child process. The engine-session shutsdown
		// when it is closed. This is a platform-agnostic way of ensuring
		// we don't leak child processes even if this process is SIGKILL'd.
		childStdin, err = proc.StdinPipe()
		if err != nil {
			cmdCancel()
			return nil, err
		}

		// Kill the child process by closing stdin, not via SIGKILL, so it has a
		// chance to drain logs.
		proc.Cancel = childStdin.Close

		// Set a long timeout to give time for any cache exports to pack layers up
		// which currently has to happen synchronously with the session.
		proc.WaitDelay = 300 * time.Second // 5 mins

		if err := proc.Start(); err != nil {
			if strings.Contains(err.Error(), "text file busy") {
				time.Sleep(100 * time.Millisecond)
				proc = nil
				stdout.Close()
				stdout = nil
				stderrPipe.Close()
				stderrBuf = nil
				childStdin.Close()
				childStdin = nil
				continue
			}
			cmdCancel()
			return nil, err
		}
		break
	}
	if proc == nil {
		cmdCancel()
		return nil, fmt.Errorf("failed to start dagger session")
	}

	defer func() {
		if rerr != nil {
			stderrContents := stderrBuf.String()
			if stderrContents != "" {
				rerr = fmt.Errorf("%s: %s", rerr, stderrContents)
			}
		}
	}()

	if cfg.LogOutput != nil {
		fmt.Fprintf(cfg.LogOutput, "OK!\nEstablishing connection to Engine... ")
	}

	// Read the connect params from stdout.
	paramCh := make(chan error, 1)
	var params ConnectParams
	go func() {
		defer close(paramCh)
		paramBytes, err := bufio.NewReader(stdout).ReadBytes('\n')
		if err != nil {
			paramCh <- err
			return
		}
		if err := json.Unmarshal(paramBytes, &params); err != nil {
			paramCh <- err
		}
	}()

	select {
	case err := <-paramCh:
		if err != nil {
			cmdCancel()
			return nil, err
		}

	case <-time.After(300 * time.Second):
		// really long time to account for extensions that need to build, though
		// that path should be optimized in future
		cmdCancel()
		return nil, fmt.Errorf("timed out waiting for session params")
	}

	if cfg.LogOutput != nil {
		fmt.Fprintln(cfg.LogOutput, "OK!")
	}

	return &cliSessionConn{
		Client:      defaultHTTPClient(&params),
		childCancel: cmdCancel,
		childProc:   proc,
	}, nil
}

// a writer that can later be turned into io.Discard
type discardableWriter struct {
	mu sync.RWMutex
	w  io.Writer
}

func (w *discardableWriter) Write(p []byte) (int, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.w.Write(p)
}

func (w *discardableWriter) Discard() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.w = io.Discard
}

type safeBuffer struct {
	bu bytes.Buffer
	mu sync.Mutex
}

func (s *safeBuffer) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bu.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bu.String()
}
