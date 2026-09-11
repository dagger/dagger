package daggercmd

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	gitattachable "github.com/dagger/dagger/engine/session/git"
	"github.com/stretchr/testify/require"
)

func TestSSHAskpassEntrypoint(t *testing.T) {
	// The helper exits before the test runner (or CLI) can print anything else.
	if os.Getenv(gitattachable.SSHAskpassSocketEnv) != "" {
		runSSHAskpass()
		panic("askpass entrypoint returned")
	}
	for _, code := range []int{http.StatusOK, http.StatusForbidden} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			listener, err := net.Listen("unix", filepath.Join(t.TempDir(), "askpass.sock"))
			require.NoError(t, err)
			defer listener.Close()
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(code)
				fmt.Fprint(w, "fixture-helper-output")
			})}
			go func() { _ = server.Serve(listener) }()
			defer server.Close()
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestSSHAskpassEntrypoint$")
			cmd.Env = append(os.Environ(), gitattachable.SSHAskpassSocketEnv+"="+listener.Addr().String())
			output, err := cmd.CombinedOutput()
			if code == http.StatusOK {
				require.NoError(t, err)
				require.Equal(t, "fixture-helper-output", string(output))
			} else {
				require.Error(t, err)
				require.Empty(t, output, "helper failures must not print response bodies or diagnostics")
			}
		})
	}
}
