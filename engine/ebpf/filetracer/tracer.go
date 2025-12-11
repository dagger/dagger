// Package filetracer provides an eBPF-based tracer for file operations
// performed by the dagger-engine process.
package filetracer

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"syscall"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/cilium/ebpf/rlimit"

	"github.com/dagger/dagger/engine/slog"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -cflags "-O2 -g -Wall -Werror -D__TARGET_ARCH_x86" -target amd64 fileops ./bpf/fileops.bpf.c
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -cflags "-O2 -g -Wall -Werror -D__TARGET_ARCH_arm64" -target arm64 fileops ./bpf/fileops.bpf.c

const (
	maxPathLen = 256

	// Operation types (must match BPF code)
	opCreate = 1
	opDelete = 2
	opStat   = 3
)

// Event represents a file operation event captured by the BPF program.
type Event struct {
	TimestampNs uint64
	Inode       uint64
	Error       int32
	Tgid        uint32
	Op          uint32
	Comm        [16]byte
	Path        [maxPathLen]byte
}

// Tracer is an eBPF-based tracer for file operations.
type Tracer struct {
	objs   fileopsObjects
	links  []link.Link
	reader *ringbuf.Reader
}

// New creates a new file operations tracer. It loads the BPF program and
// attaches to the necessary tracepoints. The tracer filters by process name.
func New() (*Tracer, error) {
	// Remove memlock limit for BPF
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("removing memlock limit: %w", err)
	}

	// Check for BTF support
	if _, err := os.Stat("/sys/kernel/btf/vmlinux"); os.IsNotExist(err) {
		return nil, errors.New("kernel BTF not available (need CONFIG_DEBUG_INFO_BTF=y)")
	}

	// Load BPF objects
	var objs fileopsObjects
	if err := loadFileopsObjects(&objs, nil); err != nil {
		var ve *ebpf.VerifierError
		if errors.As(err, &ve) {
			return nil, fmt.Errorf("BPF verifier error: %+v", ve)
		}
		return nil, fmt.Errorf("loading BPF objects: %w", err)
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
		// CREATE
		{"sys_enter_openat", objs.TpSysEnterOpenat, true},
		// DELETE
		{"sys_enter_unlinkat", objs.TpSysEnterUnlinkat, true},
		// STAT (enter + exit for return value capture)
		{"sys_enter_newfstatat", objs.TpSysEnterNewfstatat, true},
		{"sys_exit_newfstatat", objs.TpSysExitNewfstatat, true},
		{"sys_enter_statx", objs.TpSysEnterStatx, false},
		{"sys_exit_statx", objs.TpSysExitStatx, false},
	}

	for _, tp := range tracepoints {
		if err := attachTP(tp.name, tp.prog, tp.required); err != nil {
			cleanup()
			return nil, err
		}
	}

	// Open ring buffer reader
	rd, err := ringbuf.NewReader(objs.Events)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("opening ring buffer: %w", err)
	}

	return &Tracer{
		objs:   objs,
		links:  links,
		reader: rd,
	}, nil
}

// Run starts reading events from the ring buffer and logs them.
// It blocks until the context is cancelled.
func (t *Tracer) Run(ctx context.Context) {
	slog.Info("filetracer: started tracing file operations (CREATE, DELETE, STAT)")

	go func() {
		<-ctx.Done()
		t.reader.Close()
	}()

	for {
		record, err := t.reader.Read()
		if err != nil {
			if errors.Is(err, ringbuf.ErrClosed) {
				slog.Debug("filetracer: ring buffer closed, stopping")
				return
			}
			slog.Warn("filetracer: error reading from ring buffer", "error", err)
			continue
		}

		var event Event
		if err := binary.Read(bytes.NewBuffer(record.RawSample), binary.LittleEndian, &event); err != nil {
			slog.Warn("filetracer: error parsing event", "error", err)
			continue
		}

		t.logEvent(&event)
	}
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
func opName(op uint32) string {
	switch op {
	case opCreate:
		return "CREATE"
	case opDelete:
		return "DELETE"
	case opStat:
		return "STAT"
	default:
		return fmt.Sprintf("UNKNOWN(%d)", op)
	}
}

// errnoName converts a negative errno to its name (e.g., -2 -> "ENOENT")
func errnoName(errno int32) string {
	if errno >= 0 {
		return ""
	}
	// Convert to positive for lookup
	e := syscall.Errno(-errno)
	// Common errors we care about
	switch e {
	case syscall.ENOENT:
		return "ENOENT"
	case syscall.EACCES:
		return "EACCES"
	case syscall.EPERM:
		return "EPERM"
	case syscall.ENOTDIR:
		return "ENOTDIR"
	case syscall.ELOOP:
		return "ELOOP"
	case syscall.ENAMETOOLONG:
		return "ENAMETOOLONG"
	case syscall.ENODEV:
		return "ENODEV"
	case syscall.ENOMEM:
		return "ENOMEM"
	case syscall.EFAULT:
		return "EFAULT"
	case syscall.EBADF:
		return "EBADF"
	case syscall.EINVAL:
		return "EINVAL"
	case syscall.EOVERFLOW:
		return "EOVERFLOW"
	default:
		return e.Error()
	}
}

// logEvent logs a file operation event with structured fields.
func (t *Tracer) logEvent(e *Event) {
	comm := cstring(e.Comm[:])
	path := cstring(e.Path[:])

	switch e.Op {
	case opStat:
		if e.Error == 0 {
			slog.Info("filetracer",
				"op", "STAT",
				"path", path,
				"inode", e.Inode,
				"tgid", e.Tgid,
				"process", comm,
				"ts", e.TimestampNs,
			)
		} else {
			slog.Info("filetracer",
				"op", "STAT",
				"path", path,
				"error", errnoName(e.Error),
				"errno", e.Error,
				"tgid", e.Tgid,
				"process", comm,
				"ts", e.TimestampNs,
			)
		}
	default:
		slog.Info("filetracer",
			"op", opName(e.Op),
			"path", path,
			"tgid", e.Tgid,
			"process", comm,
			"ts", e.TimestampNs,
		)
	}
}

// cstring converts a null-terminated byte array to a Go string.
func cstring(b []byte) string {
	i := bytes.IndexByte(b, 0)
	if i == -1 {
		return string(b)
	}
	return string(b[:i])
}
