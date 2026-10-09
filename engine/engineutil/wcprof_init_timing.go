package engineutil

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"golang.org/x/sys/unix"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/wcprof"
)

// initTiming is a pipe on which the injected /.init reports when it started,
// spawned the user's command and reaped it (see engine.InitTimingFDEnv). It
// splits exec.processRun in wcprof into the init's start-up, the command's
// spawn, the command itself, and the exit (init exit, runc exit and stdio
// drain), which the engine can't observe on its own.
type initTiming struct {
	r, w *os.File
}

// initTimingFD is the fd the pipe's write end has in the container: runc
// --preserve-fds passes runc's extra files on from fd 3.
const initTimingFD = 3

// newInitTiming returns an initTiming when profiling an exec that runs under
// the injected /.init, or nil.
func newInitTiming(ctx context.Context, spec *specs.Spec) (*initTiming, error) {
	if !wcprof.Enabled(ctx) || spec.Process == nil || len(spec.Process.Args) == 0 || spec.Process.Args[0] != initPath {
		return nil, nil
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("init timing pipe: %w", err)
	}
	return &initTiming{r: r, w: w}, nil
}

// withEnv returns a copy of spec whose process env tells /.init where to
// report. state.spec is left as is, so nothing else run from it sees the fd.
func (t *initTiming) withEnv(spec *specs.Spec) *specs.Spec {
	if t == nil {
		return spec
	}
	cp := *spec
	proc := *spec.Process
	proc.Env = append(slices.Clip(proc.Env), fmt.Sprintf("%s=%d", engine.InitTimingFDEnv, initTimingFD))
	cp.Process = &proc
	return &cp
}

// withoutInitTimingEnv returns env without the variable withEnv adds. The
// bundle's config.json carries it, and processes exec'd into a running
// container start from that spec but get neither the fd nor /.init.
func withoutInitTimingEnv(env []string) []string {
	return slices.DeleteFunc(env, func(kv string) bool {
		return strings.HasPrefix(kv, engine.InitTimingFDEnv+"=")
	})
}

// readBundleSpec reads the spec of the container in bundle, for processes
// exec'd into it.
func readBundleSpec(bundle string) (*specs.Spec, error) {
	f, err := os.Open(filepath.Join(bundle, "config.json"))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	spec := &specs.Spec{}
	if err := json.NewDecoder(f).Decode(spec); err != nil {
		return nil, err
	}
	if spec.Process != nil {
		spec.Process.Env = withoutInitTimingEnv(spec.Process.Env)
	}
	return spec, nil
}

// extraFiles returns the files runc passes into the container, from fd 3.
func (t *initTiming) extraFiles() []*os.File {
	if t == nil {
		return nil
	}
	return []*os.File{t.w}
}

func (t *initTiming) close() {
	if t == nil {
		return
	}
	t.r.Close()
	t.w.Close()
}

// read returns /.init's report as wcprof timestamps, once runc has exited.
// ok is false when there is none, e.g. /.init failed or was killed before it
// reaped the command.
func (t *initTiming) read() (startedNS, spawnedNS, exitedNS int64, ok bool) {
	if t == nil {
		return 0, 0, 0, false
	}
	// Our copy of the write end is the only one left once runc has exited;
	// the report is one write of less than PIPE_BUF, so one read gets all of
	// it. Don't block in case a leaked copy of the write end is still open.
	t.w.Close()
	// Fd puts the file in blocking mode, so take it once, before making it
	// non-blocking.
	fd := int(t.r.Fd())
	if err := unix.SetNonblock(fd, true); err != nil {
		return 0, 0, 0, false
	}
	buf := make([]byte, 128)
	n, err := unix.Read(fd, buf)
	if err != nil || n <= 0 {
		return 0, 0, 0, false
	}
	var started, spawned, exited int64
	if _, err := fmt.Sscanf(string(buf[:n]), "%d %d %d\n", &started, &spawned, &exited); err != nil {
		return 0, 0, 0, false
	}
	nowNS, nowMono := wcprof.NowNS(), monotonicNS()
	if nowMono == 0 || started <= 0 {
		return 0, 0, 0, false
	}
	toWcprof := func(mono int64) int64 { return nowNS - (nowMono - mono) }
	return toWcprof(started), toWcprof(spawned), toWcprof(exited), true
}

func monotonicNS() int64 {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts); err != nil {
		return 0
	}
	return ts.Nano()
}

// recordProcessRunSplit records the phases of exec.processRun (op runID,
// [processStartNS, endNS]) from /.init's report:
//   - exec.initStart: runc released the workload, up to /.init's main
//     (exec of /.init and its Go runtime start);
//   - exec.processSpawn: /.init's setup and fork/exec of the command, up to
//     the command's exec;
//   - exec.workload: the command, up to /.init reaping it (user work);
//   - exec.processExit: /.init's exit, runc reaping it and exiting, and the
//     stdio drain, up to the end of the run.
//
// processStartNS comes from the kernel's coarse clock and runc writes it as
// it releases the workload, so /.init can appear to start before it; the
// boundaries are clamped to be ordered. A report that ends after the run is
// not recorded.
func recordProcessRunSplit(ctx context.Context, runID uint64, ident string, argv []string, processStartNS, endNS, startedNS, spawnedNS, exitedNS int64, outcome wcprof.Outcome) {
	b1 := max(startedNS, processStartNS)
	b2 := max(spawnedNS, b1)
	b3 := max(exitedNS, b2)
	if runID == 0 || b3 > endNS {
		return
	}
	ctx = wcprof.ContextWithOpID(ctx, runID)
	wcprof.RecordOp(ctx, wcprof.OpKindExecPhase, "exec.initStart", wcprof.OpOpts{Ident: ident}, processStartNS, b1, wcprof.OutcomeOK)
	wcprof.RecordOp(ctx, wcprof.OpKindExecPhase, "exec.processSpawn", wcprof.OpOpts{Ident: ident}, b1, b2, wcprof.OutcomeOK)
	wcprof.RecordOp(ctx, wcprof.OpKindExecPhase, "exec.workload", wcprof.OpOpts{Ident: ident, WorkType: wcprof.WorkTypeUser, Argv: argv}, b2, b3, outcome)
	wcprof.RecordOp(ctx, wcprof.OpKindExecPhase, "exec.processExit", wcprof.OpOpts{Ident: ident}, b3, endNS, wcprof.OutcomeOK)
}
