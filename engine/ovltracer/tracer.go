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

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/cilium/ebpf/rlimit"

	"github.com/dagger/dagger/engine/slog"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -cflags "-O2 -g -Wall -Werror -D__TARGET_ARCH_x86" -target amd64 ovlinuse ./bpf/ovl_inuse.bpf.c
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -cflags "-O2 -g -Wall -Werror -D__TARGET_ARCH_arm64" -target arm64 ovlinuse ./bpf/ovl_inuse.bpf.c

const (
	maxPathLen = 256
	maxDataLen = 512
)

// Event represents an overlay in-use event captured by the BPF program.
// It matches the C struct event in ovl_inuse.bpf.c.
type Event struct {
	TimestampNs uint64
	Mntns       uint32
	Tgid        uint32
	Comm        [16]byte
	InusePath   [maxPathLen]byte
	MountSrc    [maxPathLen]byte
	MountDst    [maxPathLen]byte
	MountData   [maxDataLen]byte
}

// Tracer is an eBPF-based tracer for overlay in-use errors.
type Tracer struct {
	objs    ovlinuseObjects
	tpEnter link.Link
	tpExit  link.Link
	kprobe  link.Link
	reader  *ringbuf.Reader
}

// New creates a new overlay tracer. It loads the BPF program and attaches
// to the necessary tracepoints and kprobes. Returns an error if BTF is not
// available or the required kernel symbol is not found.
func New() (*Tracer, error) {
	// Remove memlock limit for BPF
	if err := rlimit.RemoveMemlock(); err != nil {
		return nil, fmt.Errorf("removing memlock limit: %w", err)
	}

	// Check for BTF support
	if _, err := os.Stat("/sys/kernel/btf/vmlinux"); os.IsNotExist(err) {
		return nil, errors.New("kernel BTF not available (need CONFIG_DEBUG_INFO_BTF=y)")
	}

	// Find the actual symbol name (may have suffixes like .isra.0)
	kprobeSymbol, err := findKprobeSymbol()
	if err != nil {
		return nil, fmt.Errorf("finding kprobe symbol: %w", err)
	}

	slog.Debug("ovltracer: found kernel symbol", "symbol", kprobeSymbol)

	// Load BPF objects
	var objs ovlinuseObjects
	if err := loadOvlinuseObjects(&objs, nil); err != nil {
		var ve *ebpf.VerifierError
		if errors.As(err, &ve) {
			return nil, fmt.Errorf("BPF verifier error: %+v", ve)
		}
		return nil, fmt.Errorf("loading BPF objects: %w", err)
	}

	// We'll clean up on error
	cleanup := func() {
		objs.Close()
	}

	// Attach tracepoint for mount syscall entry
	tpEnter, err := link.Tracepoint("syscalls", "sys_enter_mount", objs.TpSysEnterMount, nil)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("attaching sys_enter_mount tracepoint: %w", err)
	}

	// Attach tracepoint for mount syscall exit
	tpExit, err := link.Tracepoint("syscalls", "sys_exit_mount", objs.TpSysExitMount, nil)
	if err != nil {
		tpEnter.Close()
		cleanup()
		return nil, fmt.Errorf("attaching sys_exit_mount tracepoint: %w", err)
	}

	// Attach kprobe for ovl_report_in_use
	kp, err := link.Kprobe(kprobeSymbol, objs.KpOvlReportInUse, nil)
	if err != nil {
		tpExit.Close()
		tpEnter.Close()
		cleanup()
		return nil, fmt.Errorf("attaching kprobe %s: %w", kprobeSymbol, err)
	}

	// Open ring buffer reader
	rd, err := ringbuf.NewReader(objs.Events)
	if err != nil {
		kp.Close()
		tpExit.Close()
		tpEnter.Close()
		cleanup()
		return nil, fmt.Errorf("opening ring buffer: %w", err)
	}

	return &Tracer{
		objs:    objs,
		tpEnter: tpEnter,
		tpExit:  tpExit,
		kprobe:  kp,
		reader:  rd,
	}, nil
}

// Run starts reading events from the ring buffer and logs them.
// It blocks until the context is cancelled. Run should be called
// in a goroutine.
func (t *Tracer) Run(ctx context.Context) {
	slog.Info("ovltracer: started tracing overlay in-use errors")

	// Close the reader when context is done to unblock Read()
	go func() {
		<-ctx.Done()
		t.reader.Close()
	}()

	for {
		record, err := t.reader.Read()
		if err != nil {
			if errors.Is(err, ringbuf.ErrClosed) {
				slog.Debug("ovltracer: ring buffer closed, stopping")
				return
			}
			slog.Warn("ovltracer: error reading from ring buffer", "error", err)
			continue
		}

		var event Event
		if err := binary.Read(bytes.NewBuffer(record.RawSample), binary.LittleEndian, &event); err != nil {
			slog.Warn("ovltracer: error parsing event", "error", err)
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
	if t.kprobe != nil {
		if err := t.kprobe.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing kprobe: %w", err))
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
	comm := cstring(e.Comm[:])
	inusePath := cstring(e.InusePath[:])
	mountSrc := cstring(e.MountSrc[:])
	mountDst := cstring(e.MountDst[:])
	mountData := cstring(e.MountData[:])

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

// cstring converts a null-terminated byte array to a Go string.
func cstring(b []byte) string {
	i := bytes.IndexByte(b, 0)
	if i == -1 {
		return string(b)
	}
	return string(b[:i])
}

// findKprobeSymbol finds the actual kernel symbol name for ovl_report_in_use.
// The symbol may have compiler-generated suffixes like .isra.0 or .constprop.0.
func findKprobeSymbol() (string, error) {
	data, err := os.ReadFile("/proc/kallsyms")
	if err != nil {
		return "", fmt.Errorf("reading kallsyms: %w", err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		sym := fields[2]

		// Exact match
		if sym == "ovl_report_in_use" {
			return sym, nil
		}

		// Check for suffixed versions
		if strings.HasPrefix(sym, "ovl_report_in_use.") {
			return sym, nil
		}
	}

	return "", errors.New("ovl_report_in_use symbol not found in kallsyms (is overlayfs loaded?)")
}
