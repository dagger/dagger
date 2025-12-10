// Package filetracer provides an eBPF-based tracer for file operations
// (create, rename, delete) performed by the current process.
package filetracer

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"

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
	opRename = 2
	opDelete = 3
)

// Event represents a file operation event captured by the BPF program.
type Event struct {
	TimestampNs uint64
	Tgid        uint32
	Op          uint32
	Comm        [16]byte
	Path        [maxPathLen]byte
	Path2       [maxPathLen]byte
}

// Tracer is an eBPF-based tracer for file operations.
type Tracer struct {
	objs          fileopsObjects
	tpOpenat      link.Link
	tpRenameat2   link.Link
	tpRenameat    link.Link
	tpUnlinkat    link.Link
	reader        *ringbuf.Reader
}

// New creates a new file operations tracer. It loads the BPF program and
// attaches to the necessary tracepoints. The tracer only tracks operations
// performed by the current process (by TGID).
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
	// We use comm instead of TGID because eBPF sees host PIDs but we're in a container
	targetComm := "dagger-engine"
	var commBytes [16]byte
	copy(commBytes[:], targetComm)
	zero := uint32(0)
	if err := objs.TargetComm.Put(zero, commBytes); err != nil {
		objs.Close()
		return nil, fmt.Errorf("setting target comm: %w", err)
	}

	slog.Debug("filetracer: targeting process", "comm", targetComm)

	// Track what we've attached for cleanup
	var tpOpenat, tpRenameat2, tpRenameat, tpUnlinkat link.Link
	var rd *ringbuf.Reader
	var err error

	cleanup := func() {
		if rd != nil {
			rd.Close()
		}
		if tpUnlinkat != nil {
			tpUnlinkat.Close()
		}
		if tpRenameat != nil {
			tpRenameat.Close()
		}
		if tpRenameat2 != nil {
			tpRenameat2.Close()
		}
		if tpOpenat != nil {
			tpOpenat.Close()
		}
		objs.Close()
	}

	// Attach tracepoint for openat
	tpOpenat, err = link.Tracepoint("syscalls", "sys_enter_openat", objs.TpSysEnterOpenat, nil)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("attaching sys_enter_openat tracepoint: %w", err)
	}

	// Attach tracepoint for renameat2
	tpRenameat2, err = link.Tracepoint("syscalls", "sys_enter_renameat2", objs.TpSysEnterRenameat2, nil)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("attaching sys_enter_renameat2 tracepoint: %w", err)
	}

	// Attach tracepoint for renameat (optional, some systems may not have it)
	tpRenameat, err = link.Tracepoint("syscalls", "sys_enter_renameat", objs.TpSysEnterRenameat, nil)
	if err != nil {
		slog.Debug("filetracer: sys_enter_renameat not available", "error", err)
		// Continue without it
	}

	// Attach tracepoint for unlinkat
	tpUnlinkat, err = link.Tracepoint("syscalls", "sys_enter_unlinkat", objs.TpSysEnterUnlinkat, nil)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("attaching sys_enter_unlinkat tracepoint: %w", err)
	}

	// Open ring buffer reader
	rd, err = ringbuf.NewReader(objs.Events)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("opening ring buffer: %w", err)
	}

	return &Tracer{
		objs:        objs,
		tpOpenat:    tpOpenat,
		tpRenameat2: tpRenameat2,
		tpRenameat:  tpRenameat,
		tpUnlinkat:  tpUnlinkat,
		reader:      rd,
	}, nil
}

// Run starts reading events from the ring buffer and logs them.
// It blocks until the context is cancelled.
func (t *Tracer) Run(ctx context.Context) {
	slog.Info("filetracer: started tracing file operations")

	// Close the reader when context is done to unblock Read()
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
	if t.tpUnlinkat != nil {
		if err := t.tpUnlinkat.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing unlinkat tracepoint: %w", err))
		}
	}
	if t.tpRenameat != nil {
		if err := t.tpRenameat.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing renameat tracepoint: %w", err))
		}
	}
	if t.tpRenameat2 != nil {
		if err := t.tpRenameat2.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing renameat2 tracepoint: %w", err))
		}
	}
	if t.tpOpenat != nil {
		if err := t.tpOpenat.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing openat tracepoint: %w", err))
		}
	}

	t.objs.Close()

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// logEvent logs a file operation event with structured fields.
func (t *Tracer) logEvent(e *Event) {
	comm := cstring(e.Comm[:])
	path := cstring(e.Path[:])
	path2 := cstring(e.Path2[:])

	var opName string
	switch e.Op {
	case opCreate:
		opName = "CREATE"
	case opRename:
		opName = "RENAME"
	case opDelete:
		opName = "DELETE"
	default:
		opName = fmt.Sprintf("UNKNOWN(%d)", e.Op)
	}

	if e.Op == opRename {
		slog.Info("filetracer: file operation",
			"op", opName,
			"process", comm,
			"tgid", e.Tgid,
			"old_path", path,
			"new_path", path2,
			"timestamp_ns", e.TimestampNs,
		)
	} else {
		slog.Info("filetracer: file operation",
			"op", opName,
			"process", comm,
			"tgid", e.Tgid,
			"path", path,
			"timestamp_ns", e.TimestampNs,
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
