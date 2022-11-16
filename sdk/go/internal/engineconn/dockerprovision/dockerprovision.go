package dockerprovision

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"dagger.io/dagger/internal/engineconn"
	exec "golang.org/x/sys/execabs"
)

const (
	DockerImageConnName     = "docker-image"
	DockerContainerConnName = "docker-container"
)

func init() {
	engineconn.Register(DockerImageConnName, NewDockerImage)
	engineconn.Register(DockerContainerConnName, NewDockerContainer)
}

const (
	// trim image digests to 16 characters to makeoutput more readable
	digestLen                       = 16
	containerNamePrefix             = "dagger-engine-"
	engineSessionBinPrefix          = "dagger-engine-session-"
	containerEngineSessionBinPrefix = "/usr/bin/" + engineSessionBinPrefix
)

func startEngineSession(ctx context.Context, stderr io.Writer, cmd string, args ...string) (string, io.Closer, error) {
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

	var proc *exec.Cmd
	var stdout io.ReadCloser
	var childStdin io.WriteCloser
	for i := 0; i < 10; i++ {
		proc = exec.CommandContext(ctx, cmd, args...)
		proc.Env = os.Environ()
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
