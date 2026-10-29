package wcprof

import (
	"sort"
	"sync"
)

// DumpEBPFProgram is an eBPF program a recording asked for (see
// engine/ebpf/wcbpf).
type DumpEBPFProgram struct {
	Name string `json:"name"`
	// Error is why the program couldn't be attached the last time it was
	// asked for; empty when it was.
	Error string `json:"error,omitempty"`
}

var ebpfPrograms struct {
	mu     sync.Mutex
	byName map[string]string
}

// NoteEBPFProgram records that a recording asked for the eBPF program name,
// with the error that kept it from being attached, if any. Each dump's header
// lists the programs asked for.
func NoteEBPFProgram(name string, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	ebpfPrograms.mu.Lock()
	defer ebpfPrograms.mu.Unlock()
	if ebpfPrograms.byName == nil {
		ebpfPrograms.byName = map[string]string{}
	}
	ebpfPrograms.byName[name] = msg
}

func dumpEBPFPrograms() []DumpEBPFProgram {
	ebpfPrograms.mu.Lock()
	defer ebpfPrograms.mu.Unlock()
	var ps []DumpEBPFProgram
	for name, msg := range ebpfPrograms.byName {
		ps = append(ps, DumpEBPFProgram{Name: name, Error: msg})
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].Name < ps[j].Name })
	return ps
}
