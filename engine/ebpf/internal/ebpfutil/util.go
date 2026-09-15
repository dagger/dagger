package ebpfutil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/ringbuf"
	"github.com/cilium/ebpf/rlimit"

	"github.com/dagger/dagger/engine/slog"
)

// Prepare configures the process and validates kernel capabilities needed for eBPF.
func Prepare() error {
	if err := rlimit.RemoveMemlock(); err != nil {
		return fmt.Errorf("removing memlock limit: %w", err)
	}
	if _, err := os.Stat("/sys/kernel/btf/vmlinux"); os.IsNotExist(err) {
		return errors.New("kernel BTF not available (need CONFIG_DEBUG_INFO_BTF=y)")
	}
	return nil
}

// WrapVerifierError formats verifier errors and wraps the original error with context.
func WrapVerifierError(err error, context string) error {
	var ve *ebpf.VerifierError
	if errors.As(err, &ve) {
		return fmt.Errorf("BPF verifier error: %w", ve)
	}
	if context == "" {
		return err
	}
	return fmt.Errorf("%s: %w", context, err)
}

// CString converts a null-terminated byte array to a Go string.
func CString(b []byte) string {
	i := bytes.IndexByte(b, 0)
	if i == -1 {
		return string(b)
	}
	return string(b[:i])
}

// RunRingbuf reads ring buffer events until the context is cancelled.
func RunRingbuf(ctx context.Context, reader *ringbuf.Reader, name string, handle func(raw []byte) error) {
	if reader == nil {
		return
	}
	prefix := strings.TrimSpace(name)
	if prefix != "" {
		prefix += ": "
	}

	go func() {
		<-ctx.Done()
		reader.Close()
	}()

	for {
		record, err := reader.Read()
		if err != nil {
			if errors.Is(err, ringbuf.ErrClosed) {
				slog.Debug(prefix + "ring buffer closed, stopping")
				return
			}
			slog.Warn(prefix+"error reading from ring buffer", "error", err)
			continue
		}
		if handle == nil {
			continue
		}
		if err := handle(record.RawSample); err != nil {
			slog.Warn(prefix+"error parsing event", "error", err)
		}
	}
}
