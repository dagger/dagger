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

func init() {
	engineconn.Register("docker-image", NewDockerImage)
	engineconn.Register("docker-container", NewDockerContainer)
}

const (
	// trim image digests to 16 characters to makeoutput more readable
	digestLen                = 16
	containerNamePrefix      = "dagger-engine-"
	helperBinPrefix          = "dagger-sdk-helper-"
	containerHelperBinPrefix = "/usr/bin/" + helperBinPrefix
)

func startHelper(ctx context.Context, stderr io.Writer, cmd string, args ...string) (string, io.Closer, error) {
	proc := exec.CommandContext(ctx, cmd, args...)
	proc.Env = os.Environ()
	proc.Stderr = stderr
	setPlatformOpts(proc)

	stdout, err := proc.StdoutPipe()
	if err != nil {
		return "", nil, err
	}
	defer stdout.Close() // don't need it after we read the port

	// Open a stdin pipe with the child process. The helper shutsdown
	// when it is closed. This is a platform-agnostic way of ensuring
	// we don't leak child processes even if this process is SIGKILL'd.
	childStdin, err := proc.StdinPipe()
	if err != nil {
		return "", nil, err
	}

	if err := proc.Start(); err != nil {
		return "", nil, err
	}

	// Read the port to connect to from the helper's stdout.
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
		port, err = strconv.Atoi(portStr)
		if err != nil {
			return "", nil, err
		}
	case <-ctx.Done():
		return "", nil, ctx.Err()
	}

	return fmt.Sprintf("localhost:%d", port), childStdin, nil
}
