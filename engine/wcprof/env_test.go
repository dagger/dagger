package wcprof

import (
	"flag"
	"os"
	"os/exec"
	"testing"
)

var envChild = flag.Bool("wcprof-env-child", false, "run TestEnvironmentDoesNotConfigureRecording's checks in this process")

// Recording is switched on only by the engine's --wcprof flag, the debug
// endpoint or a session's --profile, and the event cap is fixed: the
// environment changes neither. The checks run in a child test process, since
// the package's globals are set up once per process.
func TestEnvironmentDoesNotConfigureRecording(t *testing.T) {
	if *envChild {
		if GloballyEnabled() {
			t.Error("engine-global recording is on from the environment")
		}
		EnsureRecorder()
		if got := Active().maxTotal; got != defaultMaxEvents {
			t.Errorf("event cap = %d, want the fixed %d", got, defaultMaxEvents)
		}
		return
	}
	t.Parallel()
	cmd := exec.Command(os.Args[0], "-test.run=^TestEnvironmentDoesNotConfigureRecording$", "-wcprof-env-child")
	cmd.Env = append(os.Environ(), "_DAGGER_WCPROF=1", "_DAGGER_WCPROF_MAX_EVENTS=5")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child test process: %v\n%s", err, out)
	}
}
