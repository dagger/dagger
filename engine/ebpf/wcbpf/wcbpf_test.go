package wcbpf

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeProgram counts what the framework does with a program.
type fakeProgram struct {
	loadErr  error
	loads    int
	closes   int
	watched  []ExecInfo
	finished []uint64
}

func (f *fakeProgram) Load() (Loaded, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	f.loads++
	return f, nil
}

func (f *fakeProgram) WatchExec(e ExecInfo) (ExecWatch, error) {
	f.watched = append(f.watched, e)
	return f, nil
}

func (f *fakeProgram) Finish(_ context.Context, opID uint64) {
	f.finished = append(f.finished, opID)
}

func (f *fakeProgram) Close() error {
	f.closes++
	return nil
}

// registerFakes replaces the registry with fake programs for one test.
func registerFakes(t *testing.T, names ...string) map[string]*fakeProgram {
	t.Helper()
	registry.mu.Lock()
	prev := registry.programs
	registry.programs = nil
	registry.engineWide, registry.releaseEngineWide = nil, nil
	registry.mu.Unlock()
	t.Cleanup(func() {
		registry.mu.Lock()
		registry.programs = prev
		registry.engineWide, registry.releaseEngineWide = nil, nil
		registry.mu.Unlock()
	})
	fakes := map[string]*fakeProgram{}
	for _, n := range names {
		f := &fakeProgram{}
		fakes[n] = f
		Register(Program{Name: n, Load: f.Load})
	}
	return fakes
}

// A program is attached once while any recording asks for it, and detached
// when the last one stops.
func TestAcquireAttachesWhileAsked(t *testing.T) {
	f := registerFakes(t, "a", "b")["a"]
	release1, err := Acquire([]string{"a"})
	require.NoError(t, err)
	release2, err := Acquire([]string{"a", "a"})
	require.NoError(t, err)
	require.Equal(t, 1, f.loads)

	release1()
	release1()
	require.Zero(t, f.closes, "another recording still asks for it")
	release2()
	require.Equal(t, 1, f.closes)

	_, err = Acquire([]string{"a", "nope"})
	require.ErrorContains(t, err, `unknown eBPF program "nope"; known: a, b`)
	require.Equal(t, 1, f.loads, "a request with an unknown name attaches nothing")
}

// An exec measured by a program keeps it attached until the exec finishes,
// and gets what it measured on its op; an exec started while no recording
// asks for the program isn't measured.
func TestExecKeepsProgramAttached(t *testing.T) {
	fakes := registerFakes(t, "a", "b")
	exec := ExecInfo{CgroupID: 7, Init: true}
	require.Nil(t, watchExec(exec))

	release, err := Acquire([]string{"a"})
	require.NoError(t, err)
	e := watchExec(exec)
	require.NotNil(t, e)
	release()
	require.Zero(t, fakes["a"].closes, "an exec is still measured")
	require.Nil(t, watchExec(ExecInfo{CgroupID: 8}), "no recording asks for the program")

	e.Finish(context.Background(), 42)
	require.Equal(t, []ExecInfo{exec}, fakes["a"].watched)
	require.Equal(t, []uint64{42}, fakes["a"].finished)
	require.Equal(t, 1, fakes["a"].closes)
	require.Empty(t, fakes["b"].watched, "a program nobody asked for measures nothing")

	var none *Exec
	none.Finish(context.Background(), 42)
}

// A program that can't be loaded is left out without failing the recording.
func TestAcquireWithoutKernelSupport(t *testing.T) {
	f := registerFakes(t, "a")["a"]
	f.loadErr = errors.New("kernel BTF not available")
	release, err := Acquire([]string{"a"})
	require.NoError(t, err)
	require.False(t, asked())
	require.Nil(t, watchExec(ExecInfo{CgroupID: 7}))
	release()
	require.Zero(t, f.closes)
}

// Engine-wide recording's programs are replaced as a set; a program in both
// sets stays attached.
func TestSetEngineWide(t *testing.T) {
	fakes := registerFakes(t, "a", "b")
	require.NoError(t, SetEngineWide([]string{"a"}))
	require.NoError(t, SetEngineWide([]string{"a", "b"}))
	require.Equal(t, []string{"a", "b"}, EngineWide())
	require.Equal(t, 1, fakes["a"].loads)
	require.Zero(t, fakes["a"].closes)
	require.Equal(t, 1, fakes["b"].loads)

	require.Error(t, SetEngineWide([]string{"nope"}))
	require.Zero(t, fakes["b"].closes, "a failed change keeps what was asked for")
	require.Equal(t, []string{"a", "b"}, EngineWide())

	require.NoError(t, SetEngineWide(nil))
	require.Empty(t, EngineWide())
	require.Equal(t, 1, fakes["a"].closes)
	require.Equal(t, 1, fakes["b"].closes)
}
