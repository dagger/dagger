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

// The default event cap holds a test-base fresh engine's recording whole,
// with room for the ops later changes add. On a development build of 31
// Oct, a fresh engine's in-place recording needed up to about 4.7 million
// events: the old cap of 4 << 20 dropped 23k-480k of them in 7 of 16 runs,
// and lost the check's call with them.
func TestDefaultCapHoldsTestBaseFresh(t *testing.T) {
	const measured = 4_700_000
	if defaultMaxEvents < 2*measured {
		t.Errorf("default event cap = %d, want at least twice the %d events test-base's fresh engine records", defaultMaxEvents, measured)
	}
}
