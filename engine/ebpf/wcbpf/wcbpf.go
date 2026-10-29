// Package wcbpf runs eBPF programs that feed wcprof's profiles. Each program
// is compiled into the engine and registered by name. It is loaded into the
// kernel and attached only while a recording asks for it by name (a profiled
// session's, or engine-wide recording's), and detached once no recording asks
// for it and no exec it measures is left. Every profiled exec that starts
// while programs are attached is measured by each of them, and what they
// measured is recorded on its exec.run op as it ends; an exec that started
// before a program was attached isn't measured by it.
package wcbpf

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/wcprof"
)

// Program is an eBPF program that feeds wcprof.
type Program struct {
	// Name is how recordings ask for the program.
	Name string
	// Load loads the program into the kernel and attaches it.
	Load func() (Loaded, error)
}

// Loaded is a program attached to the kernel.
type Loaded interface {
	// WatchExec starts measuring the exec whose resource cgroup has the ID
	// cgroupID, before any of its processes starts.
	WatchExec(cgroupID uint64) (ExecWatch, error)
	// Close detaches the program and releases what it holds.
	Close() error
}

// ExecWatch is one exec measured by one program.
type ExecWatch interface {
	// Finish stops measuring the exec, once all its processes have exited,
	// and records what was measured on its op opID.
	Finish(ctx context.Context, opID uint64)
}

type program struct {
	Program
	// asks is how many recordings ask for the program, and execs how many
	// execs it measures; it stays attached while either isn't zero.
	asks   int
	execs  int
	loaded Loaded
}

var registry struct {
	mu       sync.Mutex
	programs map[string]*program
	// engineWide names the programs engine-wide recording asks for, and
	// releaseEngineWide releases them.
	engineWide        []string
	releaseEngineWide func()
}

// Register adds a program. Programs are registered at engine start.
func Register(p Program) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.programs == nil {
		registry.programs = map[string]*program{}
	}
	if _, ok := registry.programs[p.Name]; ok {
		panic(fmt.Sprintf("wcbpf: program %q registered twice", p.Name))
	}
	registry.programs[p.Name] = &program{Program: p}
}

// Names returns the registered programs' names, sorted.
func Names() []string {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return sortedNames()
}

func sortedNames() []string {
	ns := make([]string, 0, len(registry.programs))
	for n := range registry.programs {
		ns = append(ns, n)
	}
	slices.Sort(ns)
	return ns
}

// Acquire asks for the named programs on behalf of a recording until release
// is called, attaching those not attached yet. An unknown name is an error. A
// program that can't be loaded (a kernel without BTF, say) is logged and
// noted in the profile's header, and the recording goes on without it.
func Acquire(names []string) (release func(), err error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	var ps []*program
	for _, n := range names {
		p := registry.programs[n]
		if p == nil {
			return nil, fmt.Errorf("unknown eBPF program %q; known: %s", n, strings.Join(sortedNames(), ", "))
		}
		if !slices.Contains(ps, p) {
			ps = append(ps, p)
		}
	}
	for _, p := range ps {
		p.asks++
		if p.loaded == nil {
			p.load()
		}
	}
	return sync.OnceFunc(func() {
		registry.mu.Lock()
		defer registry.mu.Unlock()
		for _, p := range ps {
			p.asks--
			p.closeIfUnused()
		}
	}), nil
}

// SetEngineWide makes names the programs engine-wide recording asks for, in
// place of those it asked for before; none releases them. Programs in both
// stay attached.
func SetEngineWide(names []string) error {
	release, err := Acquire(names)
	if err != nil {
		return err
	}
	registry.mu.Lock()
	prev := registry.releaseEngineWide
	registry.engineWide = slices.Clone(names)
	registry.releaseEngineWide = release
	registry.mu.Unlock()
	if prev != nil {
		prev()
	}
	return nil
}

// EngineWide returns the programs engine-wide recording asks for.
func EngineWide() []string {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return slices.Clone(registry.engineWide)
}

func (p *program) load() {
	loaded, err := p.Load()
	wcprof.NoteEBPFProgram(p.Name, err)
	if err != nil {
		slog.Warn("wcbpf: eBPF program not attached", "program", p.Name, "error", err)
		return
	}
	slog.Info("wcbpf: eBPF program attached", "program", p.Name)
	p.loaded = loaded
}

func (p *program) closeIfUnused() {
	if p.asks > 0 || p.execs > 0 || p.loaded == nil {
		return
	}
	if err := p.loaded.Close(); err != nil {
		slog.Warn("wcbpf: detaching eBPF program", "program", p.Name, "error", err)
	} else {
		slog.Info("wcbpf: eBPF program detached", "program", p.Name)
	}
	p.loaded = nil
}

// Exec is an exec measured by the programs attached when it started.
type Exec struct {
	watches []execWatch
}

type execWatch struct {
	p *program
	w ExecWatch
}

// watchExec starts measuring the exec with the resource cgroup cgroupID by
// every attached program. Nil when none is attached.
func watchExec(cgroupID uint64) *Exec {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	var e Exec
	for _, n := range sortedNames() {
		p := registry.programs[n]
		if p.loaded == nil {
			continue
		}
		w, err := p.loaded.WatchExec(cgroupID)
		if err != nil {
			slog.Debug("wcbpf: exec not measured", "program", p.Name, "error", err)
			continue
		}
		p.execs++
		e.watches = append(e.watches, execWatch{p: p, w: w})
	}
	if len(e.watches) == 0 {
		return nil
	}
	return &e
}

// attached reports whether any program is attached.
func attached() bool {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	for _, p := range registry.programs {
		if p.loaded != nil {
			return true
		}
	}
	return false
}

// Finish records what each program measured on the exec's op opID, once all
// the exec's processes have exited, and stops measuring it. A nil Exec does
// nothing.
func (e *Exec) Finish(ctx context.Context, opID uint64) {
	if e == nil {
		return
	}
	for _, w := range e.watches {
		w.w.Finish(ctx, opID)
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	for _, w := range e.watches {
		w.p.execs--
		w.p.closeIfUnused()
	}
}
