package wcprof

import (
	"bytes"
	"os"
	"runtime"
	"testing"
)

func TestSelectedChoice(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"always [madvise] never", "madvise"},
		{"[always] defer defer+madvise madvise never", "always"},
		{"none voluntary (full) lazy", "full"},
		{"always madvise never", ""},
		{"", ""},
	} {
		if got := selectedChoice(c.in); got != c.want {
			t.Errorf("selectedChoice(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A dump's header describes the machine.
func TestDumpHeaderMachine(t *testing.T) {
	r := withTestRecorder(t, 0)
	var buf bytes.Buffer
	if err := r.WriteDump(&buf, false); err != nil {
		t.Fatal(err)
	}
	header, _, err := ReadDump(&buf)
	if err != nil {
		t.Fatal(err)
	}
	m := header.Machine
	if m == nil {
		t.Fatal("no machine in the header")
	}
	if m.CPUs != runtime.NumCPU() || m.GOMAXPROCS != runtime.GOMAXPROCS(0) || m.PageSize != os.Getpagesize() {
		t.Errorf("machine %+v", *m)
	}
	if release, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil && m.KernelRelease != string(bytes.TrimSpace(release)) {
		t.Errorf("kernel release %q, want %q", m.KernelRelease, bytes.TrimSpace(release))
	}
	if thp, err := os.ReadFile("/sys/kernel/mm/transparent_hugepage/enabled"); err == nil && m.THPEnabled != selectedChoice(string(thp)) {
		t.Errorf("THP enabled %q, from %q", m.THPEnabled, thp)
	}
}
