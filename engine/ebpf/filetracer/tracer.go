// Package filetracer provides an eBPF-based tracer for file operations
// performed by the dagger-engine process.
package filetracer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"

	dagebpf "github.com/dagger/dagger/engine/ebpf"
	"github.com/dagger/dagger/engine/ebpf/internal/ebpfutil"
	"github.com/dagger/dagger/engine/slog"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -cflags "-O2 -g -Wall -Werror -D__TARGET_ARCH_x86 -I../bpf" -target amd64 fileops ./bpf/fileops.bpf.c
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -cflags "-O2 -g -Wall -Werror -D__TARGET_ARCH_arm64 -I../bpf" -target arm64 fileops ./bpf/fileops.bpf.c

const (
	maxPathLen    = 128
	maxStackDepth = 16

	// Operation types (must match BPF code)
	opMount             = 1
	opUmount            = 2
	opUnlinkat          = 3
	opMkdirat           = 4
	opStat              = 5
	opOvlWorkdirCreate  = 6
	opOvlWorkdirCleanup = 7
	opVfsMkdir          = 8
	opVfsRmdir          = 9

	// AT_REMOVEDIR flag for unlinkat
	atRemovedir = 0x200
)

// Event represents a file operation event captured by the BPF program.
type Event struct {
	TimestampNs uint64
	DurationNs  uint64
	Stack       [maxStackDepth]uint64
	Error       int32
	Tgid        uint32
	Op          uint32
	Flags       uint32
	StackSize   uint32
	Comm        [16]byte
	Path        [maxPathLen]byte
	Path2       [maxPathLen]byte
}

// ksym represents a kernel symbol from /proc/kallsyms
type ksym struct {
	addr uint64
	name string
}

// ksyms is a sorted list of kernel symbols for address lookup
type ksyms []ksym

func (k ksyms) Len() int           { return len(k) }
func (k ksyms) Less(i, j int) bool { return k[i].addr < k[j].addr }
func (k ksyms) Swap(i, j int)      { k[i], k[j] = k[j], k[i] }

// resolve finds the symbol name for a given address
func (k ksyms) resolve(addr uint64) string {
	if len(k) == 0 {
		return fmt.Sprintf("0x%x", addr)
	}
	// Binary search for the largest address <= addr
	i := sort.Search(len(k), func(i int) bool { return k[i].addr > addr })
	if i == 0 {
		return fmt.Sprintf("0x%x", addr)
	}
	sym := k[i-1]
	offset := addr - sym.addr
	if offset == 0 {
		return sym.name
	}
	return fmt.Sprintf("%s+0x%x", sym.name, offset)
}

// loadKsyms loads kernel symbols from /proc/kallsyms
func loadKsyms() ksyms {
	f, err := os.Open("/proc/kallsyms")
	if err != nil {
		slog.Debug("filetracer: failed to open kallsyms", "error", err)
		return nil
	}
	defer f.Close()

	var syms ksyms
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		addr, err := strconv.ParseUint(fields[0], 16, 64)
		if err != nil {
			continue
		}
		// Only include text symbols (functions)
		typ := fields[1]
		if typ != "t" && typ != "T" {
			continue
		}
		syms = append(syms, ksym{addr: addr, name: fields[2]})
	}
	sort.Sort(syms)
	slog.Debug("filetracer: loaded kernel symbols", "count", len(syms))
	return syms
}

// Tracer is an eBPF-based tracer for file operations.
type Tracer struct {
	objs   fileopsObjects
	links  []link.Link
	reader *ringbuf.Reader
	ksyms  ksyms
}

// New creates a new file operations tracer. It loads the BPF program and
// attaches to the necessary tracepoints. The tracer filters by process name.
func New() (dagebpf.Tracer, error) {
	if err := ebpfutil.Prepare(); err != nil {
		return nil, err
	}

	// Load BPF objects
	var objs fileopsObjects
	if err := loadFileopsObjects(&objs, nil); err != nil {
		return nil, ebpfutil.WrapVerifierError(err, "loading BPF objects")
	}

	// Set target comm (process name) to filter by
	targetComm := "dagger-engine"
	var commBytes [16]byte
	copy(commBytes[:], targetComm)
	zero := uint32(0)
	if err := objs.TargetComm.Put(zero, commBytes); err != nil {
		objs.Close()
		return nil, fmt.Errorf("setting target comm: %w", err)
	}

	slog.Debug("filetracer: targeting process", "comm", targetComm)

	var links []link.Link

	cleanup := func() {
		for _, l := range links {
			l.Close()
		}
		objs.Close()
	}

	// Helper to attach a tracepoint
	attachTP := func(name string, prog *ebpf.Program, required bool) error {
		l, err := link.Tracepoint("syscalls", name, prog, nil)
		if err != nil {
			if required {
				return fmt.Errorf("attaching %s: %w", name, err)
			}
			slog.Debug("filetracer: tracepoint not available", "name", name, "error", err)
			return nil
		}
		links = append(links, l)
		return nil
	}

	// Attach tracepoints
	tracepoints := []struct {
		name     string
		prog     *ebpf.Program
		required bool
	}{
		// MOUNT
		{"sys_enter_mount", objs.TpSysEnterMount, true},
		{"sys_exit_mount", objs.TpSysExitMount, true},
		// UMOUNT
		{"sys_enter_umount", objs.TpSysEnterUmount, true},
		{"sys_exit_umount", objs.TpSysExitUmount, true},
		// UNLINKAT (handles both unlink and rmdir)
		{"sys_enter_unlinkat", objs.TpSysEnterUnlinkat, true},
		{"sys_exit_unlinkat", objs.TpSysExitUnlinkat, true},
		// MKDIRAT
		{"sys_enter_mkdirat", objs.TpSysEnterMkdirat, true},
		{"sys_exit_mkdirat", objs.TpSysExitMkdirat, true},
		// STAT
		{"sys_enter_newfstatat", objs.TpSysEnterNewfstatat, true},
		{"sys_exit_newfstatat", objs.TpSysExitNewfstatat, true},
	}

	for _, tp := range tracepoints {
		if err := attachTP(tp.name, tp.prog, tp.required); err != nil {
			cleanup()
			return nil, err
		}
	}

	// Helper to attach a kprobe
	attachKprobe := func(symbol string, prog *ebpf.Program, required bool) error {
		l, err := link.Kprobe(symbol, prog, nil)
		if err != nil {
			if required {
				return fmt.Errorf("attaching kprobe %s: %w", symbol, err)
			}
			slog.Debug("filetracer: kprobe not available", "symbol", symbol, "error", err)
			return nil
		}
		links = append(links, l)
		slog.Debug("filetracer: attached kprobe", "symbol", symbol)
		return nil
	}

	// Helper to attach a kretprobe
	attachKretprobe := func(symbol string, prog *ebpf.Program, required bool) error {
		l, err := link.Kretprobe(symbol, prog, nil)
		if err != nil {
			if required {
				return fmt.Errorf("attaching kretprobe %s: %w", symbol, err)
			}
			slog.Debug("filetracer: kretprobe not available", "symbol", symbol, "error", err)
			return nil
		}
		links = append(links, l)
		slog.Debug("filetracer: attached kretprobe", "symbol", symbol)
		return nil
	}

	// Attach kprobes for overlay workdir operations
	kprobes := []struct {
		symbol    string
		kprobe    *ebpf.Program
		kretprobe *ebpf.Program
		required  bool
	}{
		{"ovl_workdir_create", objs.KpOvlWorkdirCreate, objs.KretpOvlWorkdirCreate, false},
		{"ovl_workdir_cleanup", objs.KpOvlWorkdirCleanup, objs.KretpOvlWorkdirCleanup, false},
		{"vfs_mkdir", objs.KpVfsMkdir, objs.KretpVfsMkdir, false},
		{"vfs_rmdir", objs.KpVfsRmdir, objs.KretpVfsRmdir, false},
	}

	for _, kp := range kprobes {
		if err := attachKprobe(kp.symbol, kp.kprobe, kp.required); err != nil {
			cleanup()
			return nil, err
		}
		if kp.kretprobe != nil {
			if err := attachKretprobe(kp.symbol, kp.kretprobe, kp.required); err != nil {
				cleanup()
				return nil, err
			}
		}
	}

	// Open ring buffer reader
	rd, err := ringbuf.NewReader(objs.Events)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("opening ring buffer: %w", err)
	}

	// Load kernel symbols for stack trace resolution
	syms := loadKsyms()

	return &Tracer{
		objs:   objs,
		links:  links,
		reader: rd,
		ksyms:  syms,
	}, nil
}

// Run starts reading events from the ring buffer and logs them.
// It blocks until the context is cancelled.
func (t *Tracer) Run(ctx context.Context) {
	slog.Info("filetracer: started tracing MOUNT, UMOUNT, UNLINKAT, MKDIRAT, STAT")
	ebpfutil.RunRingbuf(ctx, t.reader, "filetracer", func(raw []byte) error {
		var event Event
		if err := binary.Read(bytes.NewBuffer(raw), binary.LittleEndian, &event); err != nil {
			return err
		}
		t.logEvent(&event)
		return nil
	})
}

// Close releases all resources held by the tracer.
func (t *Tracer) Close() error {
	var errs []error

	if t.reader != nil {
		if err := t.reader.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing ring buffer: %w", err))
		}
	}

	for _, l := range t.links {
		if err := l.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing link: %w", err))
		}
	}

	t.objs.Close()

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// opName returns the human-readable name for an operation.
func opName(op uint32, flags uint32) string {
	switch op {
	case opMount:
		return "MOUNT"
	case opUmount:
		return "UMOUNT"
	case opUnlinkat:
		if flags&atRemovedir != 0 {
			return "RMDIR"
		}
		return "UNLINK"
	case opMkdirat:
		return "MKDIR"
	case opStat:
		return "STAT"
	case opOvlWorkdirCreate:
		return "OVL_WORKDIR_CREATE"
	case opOvlWorkdirCleanup:
		return "OVL_WORKDIR_CLEANUP"
	case opVfsMkdir:
		return "VFS_MKDIR"
	case opVfsRmdir:
		return "VFS_RMDIR"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", op)
	}
}

// errnoName converts a negative errno to its name
func errnoName(errno int32) string {
	if errno >= 0 {
		return ""
	}
	e := syscall.Errno(-errno)
	switch e {
	case syscall.ENOENT:
		return "ENOENT"
	case syscall.EACCES:
		return "EACCES"
	case syscall.EPERM:
		return "EPERM"
	case syscall.ENOTDIR:
		return "ENOTDIR"
	case syscall.EISDIR:
		return "EISDIR"
	case syscall.EEXIST:
		return "EEXIST"
	case syscall.ENOTEMPTY:
		return "ENOTEMPTY"
	case syscall.EBUSY:
		return "EBUSY"
	case syscall.EINVAL:
		return "EINVAL"
	case syscall.EROFS:
		return "EROFS"
	default:
		return e.Error()
	}
}

// formatStack formats a kernel stack trace as a string with symbol resolution
func (t *Tracer) formatStack(stack [maxStackDepth]uint64, size uint32) string {
	if size == 0 {
		return ""
	}
	var syms []string
	for i := uint32(0); i < size && i < maxStackDepth; i++ {
		if stack[i] == 0 {
			break
		}
		syms = append(syms, t.ksyms.resolve(stack[i]))
	}
	return strings.Join(syms, " <- ")
}

// logEvent logs a file operation event with structured fields.
func (t *Tracer) logEvent(e *Event) {
	comm := ebpfutil.CString(e.Comm[:])
	path := ebpfutil.CString(e.Path[:])
	path2 := ebpfutil.CString(e.Path2[:])
	op := opName(e.Op, e.Flags)

	switch e.Op {
	case opMount:
		if e.Error == 0 {
			slog.Info("filetracer",
				"op", op,
				"target", path,
				"options", path2,
				"flags", fmt.Sprintf("0x%x", e.Flags),
				"tgid", e.Tgid,
				"process", comm,
			)
		} else {
			slog.Info("filetracer",
				"op", op,
				"target", path,
				"options", path2,
				"flags", fmt.Sprintf("0x%x", e.Flags),
				"error", errnoName(e.Error),
				"tgid", e.Tgid,
				"process", comm,
			)
		}
	case opUmount:
		if e.Error == 0 {
			slog.Info("filetracer",
				"op", op,
				"target", path,
				"flags", fmt.Sprintf("0x%x", e.Flags),
				"tgid", e.Tgid,
				"process", comm,
			)
		} else {
			slog.Info("filetracer",
				"op", op,
				"target", path,
				"flags", fmt.Sprintf("0x%x", e.Flags),
				"error", errnoName(e.Error),
				"tgid", e.Tgid,
				"process", comm,
			)
		}
	case opVfsMkdir, opVfsRmdir:
		// VFS operations have flags=1 for entry, flags=0 for exit
		phase := "exit"
		if e.Flags == 1 {
			phase = "entry"
		}
		stack := t.formatStack(e.Stack, e.StackSize)
		if e.Error == 0 {
			if stack != "" {
				slog.Info("filetracer",
					"op", op,
					"phase", phase,
					"path", path,
					"stack", stack,
					"tgid", e.Tgid,
					"process", comm,
				)
			} else {
				slog.Info("filetracer",
					"op", op,
					"phase", phase,
					"path", path,
					"tgid", e.Tgid,
					"process", comm,
				)
			}
		} else {
			if stack != "" {
				slog.Info("filetracer",
					"op", op,
					"phase", phase,
					"path", path,
					"error", errnoName(e.Error),
					"stack", stack,
					"tgid", e.Tgid,
					"process", comm,
				)
			} else {
				slog.Info("filetracer",
					"op", op,
					"phase", phase,
					"path", path,
					"error", errnoName(e.Error),
					"tgid", e.Tgid,
					"process", comm,
				)
			}
		}
	case opOvlWorkdirCreate, opOvlWorkdirCleanup:
		if e.Error == 0 {
			slog.Info("filetracer",
				"op", op,
				"tgid", e.Tgid,
				"process", comm,
			)
		} else {
			slog.Info("filetracer",
				"op", op,
				"error", errnoName(e.Error),
				"tgid", e.Tgid,
				"process", comm,
			)
		}
	default:
		if e.Error == 0 {
			slog.Info("filetracer",
				"op", op,
				"path", path,
				"tgid", e.Tgid,
				"process", comm,
			)
		} else {
			slog.Info("filetracer",
				"op", op,
				"path", path,
				"error", errnoName(e.Error),
				"tgid", e.Tgid,
				"process", comm,
			)
		}
	}
}
