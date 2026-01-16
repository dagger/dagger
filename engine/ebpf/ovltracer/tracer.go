// Package ovltracer provides an eBPF-based tracer for catching overlayfs
// "in-use" errors (EBUSY). When overlayfs detects a directory conflict
// (e.g., trying to use an upperdir already in use), this tracer captures
// the event and logs the paths involved.
package ovltracer

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"

	"github.com/dagger/dagger/engine/ebpf"
	"github.com/dagger/dagger/engine/ebpf/internal/ebpfutil"
	"github.com/dagger/dagger/engine/slog"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -cflags "-O2 -g -Wall -Werror -D__TARGET_ARCH_x86 -I../bpf" -target amd64 ovlinuse ./bpf/ovl_inuse.bpf.c
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -cflags "-O2 -g -Wall -Werror -D__TARGET_ARCH_arm64 -I../bpf" -target arm64 ovlinuse ./bpf/ovl_inuse.bpf.c

const (
	maxPathLen    = 256
	maxDataLen    = 512
	dentryNameLen = 64
)

// Event represents an overlay in-use event captured by the BPF program.
// It matches the C struct event in ovl_inuse.bpf.c.
type Event struct {
	TimestampNs uint64
	Mntns       uint32
	Tgid        uint32
	Comm        [16]byte
	DentryName0 [dentryNameLen]byte // The dentry itself
	DentryName1 [dentryNameLen]byte // Parent
	DentryName2 [dentryNameLen]byte // Grandparent
	MountSrc    [maxPathLen]byte
	MountDst    [maxPathLen]byte
	MountData   [maxDataLen]byte
}

// Tracer is an eBPF-based tracer for overlay in-use errors.
type Tracer struct {
	objs         ovlinuseObjects
	tpEnter      link.Link
	tpExit       link.Link
	kpTrylock    link.Link
	kretpTrylock link.Link
	kpIsInuse    link.Link
	kretpIsInuse link.Link
	reader       *ringbuf.Reader
}

// New creates a new overlay tracer. It loads the BPF program and attaches
// to the necessary tracepoints and kprobes. Returns an error if BTF is not
// available or the required kernel symbol is not found.
func New() (ebpf.Tracer, error) {
	if err := ebpfutil.Prepare(); err != nil {
		return nil, err
	}

	// Find the actual symbol names (may have suffixes like .isra.0)
	trylockSymbol, isInuseSymbol, err := findKprobeSymbols()
	if err != nil {
		return nil, fmt.Errorf("finding kprobe symbols: %w", err)
	}

	slog.Debug("ovltracer: found kernel symbols", "trylock", trylockSymbol, "is_inuse", isInuseSymbol)

	// Load BPF objects
	var objs ovlinuseObjects
	if err := loadOvlinuseObjects(&objs, nil); err != nil {
		return nil, ebpfutil.WrapVerifierError(err, "loading BPF objects")
	}

	// Track what we've attached for cleanup on error
	var tpEnter, tpExit link.Link
	var kpTrylock, kretpTrylock link.Link
	var kpIsInuse, kretpIsInuse link.Link
	var rd *ringbuf.Reader

	cleanup := func() {
		if rd != nil {
			rd.Close()
		}
		if kretpIsInuse != nil {
			kretpIsInuse.Close()
		}
		if kpIsInuse != nil {
			kpIsInuse.Close()
		}
		if kretpTrylock != nil {
			kretpTrylock.Close()
		}
		if kpTrylock != nil {
			kpTrylock.Close()
		}
		if tpExit != nil {
			tpExit.Close()
		}
		if tpEnter != nil {
			tpEnter.Close()
		}
		objs.Close()
	}

	// Attach tracepoint for mount syscall entry
	tpEnter, err = link.Tracepoint("syscalls", "sys_enter_mount", objs.TpSysEnterMount, nil)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("attaching sys_enter_mount tracepoint: %w", err)
	}

	// Attach tracepoint for mount syscall exit
	tpExit, err = link.Tracepoint("syscalls", "sys_exit_mount", objs.TpSysExitMount, nil)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("attaching sys_exit_mount tracepoint: %w", err)
	}

	// Attach kprobe/kretprobe for ovl_inuse_trylock (optional - may not exist on all kernels)
	if trylockSymbol != "" {
		kpTrylock, err = link.Kprobe(trylockSymbol, objs.KpOvlInuseTrylock, nil)
		if err != nil {
			slog.Debug("ovltracer: failed to attach trylock kprobe", "error", err)
		} else {
			kretpTrylock, err = link.Kretprobe(trylockSymbol, objs.KretpOvlInuseTrylock, nil)
			if err != nil {
				kpTrylock.Close()
				kpTrylock = nil
				slog.Debug("ovltracer: failed to attach trylock kretprobe", "error", err)
			}
		}
	}

	// Attach kprobe/kretprobe for ovl_is_inuse (optional - may not exist on all kernels)
	if isInuseSymbol != "" {
		kpIsInuse, err = link.Kprobe(isInuseSymbol, objs.KpOvlIsInuse, nil)
		if err != nil {
			slog.Debug("ovltracer: failed to attach is_inuse kprobe", "error", err)
		} else {
			kretpIsInuse, err = link.Kretprobe(isInuseSymbol, objs.KretpOvlIsInuse, nil)
			if err != nil {
				kpIsInuse.Close()
				kpIsInuse = nil
				slog.Debug("ovltracer: failed to attach is_inuse kretprobe", "error", err)
			}
		}
	}

	// Need at least one overlay probe to be useful
	if kpTrylock == nil && kpIsInuse == nil {
		cleanup()
		return nil, errors.New("failed to attach any overlay kprobes (ovl_inuse_trylock or ovl_is_inuse)")
	}

	// Open ring buffer reader
	rd, err = ringbuf.NewReader(objs.Events)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("opening ring buffer: %w", err)
	}

	return &Tracer{
		objs:         objs,
		tpEnter:      tpEnter,
		tpExit:       tpExit,
		kpTrylock:    kpTrylock,
		kretpTrylock: kretpTrylock,
		kpIsInuse:    kpIsInuse,
		kretpIsInuse: kretpIsInuse,
		reader:       rd,
	}, nil
}

// Run starts reading events from the ring buffer and logs them.
// It blocks until the context is cancelled. Run should be called
// in a goroutine.
func (t *Tracer) Run(ctx context.Context) {
	slog.Info("ovltracer: started tracing overlay in-use errors")
	ebpfutil.RunRingbuf(ctx, t.reader, "ovltracer", func(raw []byte) error {
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
	if t.kretpIsInuse != nil {
		if err := t.kretpIsInuse.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing is_inuse kretprobe: %w", err))
		}
	}
	if t.kpIsInuse != nil {
		if err := t.kpIsInuse.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing is_inuse kprobe: %w", err))
		}
	}
	if t.kretpTrylock != nil {
		if err := t.kretpTrylock.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing trylock kretprobe: %w", err))
		}
	}
	if t.kpTrylock != nil {
		if err := t.kpTrylock.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing trylock kprobe: %w", err))
		}
	}
	if t.tpExit != nil {
		if err := t.tpExit.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing sys_exit_mount tracepoint: %w", err))
		}
	}
	if t.tpEnter != nil {
		if err := t.tpEnter.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing sys_enter_mount tracepoint: %w", err))
		}
	}

	t.objs.Close()

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}

// logEvent logs an overlay in-use event with structured fields.
func (t *Tracer) logEvent(e *Event) {
	comm := ebpfutil.CString(e.Comm[:])
	mountSrc := ebpfutil.CString(e.MountSrc[:])
	mountDst := ebpfutil.CString(e.MountDst[:])
	mountData := ebpfutil.CString(e.MountData[:])

	// Build path from dentry names: grandparent/parent/child
	name0 := ebpfutil.CString(e.DentryName0[:])
	name1 := ebpfutil.CString(e.DentryName1[:])
	name2 := ebpfutil.CString(e.DentryName2[:])

	var inusePath string
	if name2 != "" {
		inusePath = name2 + "/" + name1 + "/" + name0
	} else if name1 != "" {
		inusePath = name1 + "/" + name0
	} else {
		inusePath = name0
	}

	slog.Warn("ovltracer: OVERLAY IN-USE DETECTED",
		"process", comm,
		"tgid", e.Tgid,
		"mntns", e.Mntns,
		"inuse_path", inusePath,
		"mount_src", mountSrc,
		"mount_dst", mountDst,
		"mount_data", mountData,
		"timestamp_ns", e.TimestampNs,
	)
}

// findKprobeSymbols finds the actual kernel symbol names for ovl_inuse_trylock
// and ovl_is_inuse. The symbols may have compiler-generated suffixes like
// .isra.0 or .constprop.0. Returns empty string for symbols not found.
func findKprobeSymbols() (trylockSym, isInuseSym string, err error) {
	data, err := os.ReadFile("/proc/kallsyms")
	if err != nil {
		return "", "", fmt.Errorf("reading kallsyms: %w", err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		sym := fields[2]

		// Check for ovl_inuse_trylock
		if trylockSym == "" {
			if sym == "ovl_inuse_trylock" || strings.HasPrefix(sym, "ovl_inuse_trylock.") {
				trylockSym = sym
			}
		}

		// Check for ovl_is_inuse
		if isInuseSym == "" {
			if sym == "ovl_is_inuse" || strings.HasPrefix(sym, "ovl_is_inuse.") {
				isInuseSym = sym
			}
		}

		// Exit early if both found
		if trylockSym != "" && isInuseSym != "" {
			break
		}
	}

	// At least one symbol must exist
	if trylockSym == "" && isInuseSym == "" {
		return "", "", errors.New("neither ovl_inuse_trylock nor ovl_is_inuse found in kallsyms (is overlayfs loaded?)")
	}

	return trylockSym, isInuseSym, nil
}
