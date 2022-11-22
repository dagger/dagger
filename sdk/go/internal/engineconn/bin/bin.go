package bin

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"dagger.io/dagger/internal/engineconn"
)

func init() {
	engineconn.Register("bin", New)
}

func New(u *url.URL) (engineconn.EngineConn, error) {
	path := u.Host + u.Path
	var err error
	if path == "" {
		path = "dagger-engine-session"
	}
	path, err = exec.LookPath(path)
	if err != nil {
		return nil, err
	}

	return &Bin{
		path: path,
	}, nil
}

type Bin struct {
	path       string
	childStdin io.Closer
}

func (c *Bin) Connect(ctx context.Context, cfg *engineconn.Config) (*http.Client, error) {
	args := []string{}
	if cfg.Workdir != "" {
		args = append(args, "--workdir", cfg.Workdir)
	}
	if cfg.ConfigPath != "" {
		args = append(args, "--project", cfg.ConfigPath)
	}

	addr, childStdin, err := StartEngineSession(ctx, cfg.LogOutput, "", c.path, args...)
	if err != nil {
		return nil, err
	}
	c.childStdin = childStdin

	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("tcp", addr)
			},
		},
	}, nil
}

func (c *Bin) Addr() string {
	return "http://dagger"
}

func (c *Bin) Close() error {
	if c.childStdin != nil {
		return c.childStdin.Close()
	}
	return nil
}

func StartEngineSession(ctx context.Context, stderr io.Writer, defaultDaggerRunnerHost string, cmd string, args ...string) (string, io.Closer, error) {
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
	// engine-session binary needs to be created and even then only if many
	// threads within this process are trying to provision it at the same time.
	daggerRunnerHost, ok := os.LookupEnv("DAGGER_RUNNER_HOST")
	if !ok {
		daggerRunnerHost = defaultDaggerRunnerHost
	}
	env := os.Environ()
	if daggerRunnerHost != "" {
		env = append(env, "DAGGER_RUNNER_HOST="+daggerRunnerHost)
	}

	var proc *exec.Cmd
	var stdout io.ReadCloser
	var childStdin io.WriteCloser
	for i := 0; i < 10; i++ {
		proc = exec.CommandContext(ctx, cmd, args...)
		proc.Env = env
		proc.Stderr = stderr
		setPlatformOpts(proc)

		var err error
		stdout, err = proc.StdoutPipe()
		if err != nil {
			return "", nil, err
		}
		defer stdout.Close() // don't need it after we read the port

		// Open a stdin pipe with the child process. The engine-session shutsdown
		// when it is closed. This is a platform-agnostic way of ensuring
		// we don't leak child processes even if this process is SIGKILL'd.
		childStdin, err = proc.StdinPipe()
		if err != nil {
			return "", nil, err
		}

		if err := proc.Start(); err != nil {
			if strings.Contains(err.Error(), "text file busy") {
				time.Sleep(100 * time.Millisecond)
				proc = nil
				stdout.Close()
				stdout = nil
				childStdin.Close()
				childStdin = nil
				continue
			}
			return "", nil, err
		}
		break
	}
	if proc == nil {
		return "", nil, fmt.Errorf("failed to start engine session")
	}

	// Read the port to connect to from the engine-session's stdout.
	portCh := make(chan string, 1)
	var portErr error
	go func() {
		defer close(portCh)
		portStr, err := bufio.NewReader(stdout).ReadString('\n')
		if err != nil {
			portErr = err
			return
		}
		portCh <- portStr
	}()

	ctx, cancel := context.WithTimeout(ctx, 300*time.Second) // really long time to account for extensions that need to build, though that path should be optimized in future
	defer cancel()
	var port int
	select {
	case portStr := <-portCh:
		if portErr != nil {
			return "", nil, portErr
		}
		portStr = strings.TrimSpace(portStr)
		var err error
		port, err = strconv.Atoi(portStr)
		if err != nil {
			return "", nil, err
		}
	case <-ctx.Done():
		return "", nil, ctx.Err()
	}

	return fmt.Sprintf("localhost:%d", port), childStdin, nil
}
