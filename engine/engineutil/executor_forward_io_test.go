package engineutil

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type closingBuffer struct{ bytes.Buffer }

func (*closingBuffer) Close() error { return nil }

// forwardIO hands a process's output to its writers whole, however much
// of it there is, and leaves a missing writer missing.
func TestForwardIOCopiesProcessOutput(t *testing.T) {
	big := strings.Repeat("0123456789abcde\n", 16<<10) // 256 KiB, many copy buffers' worth
	var stdout, stderr closingBuffer
	cmd := exec.Command("sh", "-c", `yes 0123456789abcde | head -c 262144; printf err >&2`)
	(&forwardIO{stdout: &stdout, stderr: &stderr}).Set(cmd)
	require.NoError(t, cmd.Run())
	require.Equal(t, big, stdout.String())
	require.Equal(t, "err", stderr.String())

	cmd = exec.Command("sh", "-c", "echo dropped; echo dropped >&2")
	(&forwardIO{}).Set(cmd)
	require.Nil(t, cmd.Stdout)
	require.Nil(t, cmd.Stderr)
	require.NoError(t, cmd.Run())
}
