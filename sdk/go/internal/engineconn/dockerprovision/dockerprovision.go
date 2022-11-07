package dockerprovision

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

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
	// TODO: timeouts and such
	portStr, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		return "", nil, err
	}
	portStr = strings.TrimSpace(portStr)
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", nil, err
	} // TODO: validation it's in the right range

	return fmt.Sprintf("localhost:%d", port), childStdin, nil
}
