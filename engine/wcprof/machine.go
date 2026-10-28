package wcprof

import (
	"os"
	"runtime"
	"strings"
	"sync"
)

// DumpMachine is what a dump records about the machine its engine ran on,
// read once per engine process. A fact that couldn't be read is left out.
type DumpMachine struct {
	// KernelRelease and KernelVersion are the kernel's release (as uname -r)
	// and its build string (as uname -v), which names the preemption models
	// it was built with (PREEMPT, PREEMPT_DYNAMIC, PREEMPT_RT).
	KernelRelease string `json:"kernel_release,omitempty"`
	KernelVersion string `json:"kernel_version,omitempty"`
	// Preempt is the preemption model in use, when the kernel can switch it
	// at boot (PREEMPT_DYNAMIC) and debugfs is readable: none, voluntary,
	// full or lazy.
	Preempt  string `json:"preempt,omitempty"`
	PageSize int    `json:"page_size"`
	// THPEnabled and THPDefrag are the transparent huge page settings
	// (always, madvise or never; and the defrag policy).
	THPEnabled string `json:"thp_enabled,omitempty"`
	THPDefrag  string `json:"thp_defrag,omitempty"`
	// CPUs are the CPUs the engine process may run on, and GOMAXPROCS how
	// many it runs Go code on at once.
	CPUs       int `json:"cpus"`
	GOMAXPROCS int `json:"gomaxprocs"`
}

var machineFacts = sync.OnceValue(readMachine)

func readMachine() *DumpMachine {
	read := func(path string) string {
		bs, err := os.ReadFile(path)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(bs))
	}
	return &DumpMachine{
		KernelRelease: read("/proc/sys/kernel/osrelease"),
		KernelVersion: read("/proc/sys/kernel/version"),
		Preempt:       selectedChoice(read("/sys/kernel/debug/sched/preempt")),
		PageSize:      os.Getpagesize(),
		THPEnabled:    selectedChoice(read("/sys/kernel/mm/transparent_hugepage/enabled")),
		THPDefrag:     selectedChoice(read("/sys/kernel/mm/transparent_hugepage/defrag")),
		CPUs:          runtime.NumCPU(),
		GOMAXPROCS:    runtime.GOMAXPROCS(0),
	}
}

// selectedChoice returns the marked choice of a kernel setting that lists
// its choices: bracketed, as "always [madvise] never", or in parentheses, as
// "none voluntary (full) lazy"; "" when none is.
func selectedChoice(s string) string {
	for _, f := range strings.Fields(s) {
		if len(f) > 2 && (f[0] == '[' && f[len(f)-1] == ']' || f[0] == '(' && f[len(f)-1] == ')') {
			return f[1 : len(f)-1]
		}
	}
	return ""
}
