package dagui

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync/atomic"

	"github.com/dagger/dagger/engine/slog"
	telemetry "github.com/dagger/otel-go"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// Primary output must survive until the final render, but retaining SDK records
// also retains their resource/attribute graphs and arbitrarily large bodies.
// Keep only a small byte prefix in memory and spill the rest, preserving stream
// order. Readers replay in bounded chunks, including for a single huge record.
//
// Spilling is an optimization, never a precondition for printing output: if
// temporary storage cannot be created or written (an unwritable TMPDIR, a full
// disk), the rest of the output stays in memory, as it did before spilling.
const primaryLogMemoryLimit = 64 << 10

// primaryLogChunk is a run of consecutive output on one stream. Records on the
// same stream coalesce, so many small records cost one chunk.
type primaryLogChunk struct {
	stream int64
	body   []byte
}

type primaryLogBuffer struct {
	// chunks is the whole output while nothing has spilled, and the tail after
	// the file once spilling has failed.
	chunks []primaryLogChunk
	bytes  int
	file   *os.File
	// fileSize is the length of the complete records in file.
	fileSize int64
	// spillFailed keeps all further output in memory.
	spillFailed bool
	cleanup     runtime.Cleanup
	err         error
}

// primarySpillWarned limits the spill-failure warning to once per process.
var primarySpillWarned atomic.Bool

func (db *DB) appendPrimaryLog(id SpanID, record sdklog.Record) {
	db.primaryLogsMu.Lock()
	defer db.primaryLogsMu.Unlock()
	if db.primaryLogsClosed {
		return
	}
	body, ok := LogBodyString(record)
	if !ok || body == "" {
		return
	}
	var stream int64
	record.WalkAttributes(func(attr otellog.KeyValue) bool {
		if attr.Key == telemetry.StdioStreamAttr {
			stream, _ = LogValueInt64(attr.Value)
			return false
		}
		return true
	})
	buf := db.primaryLogs[id]
	if buf == nil {
		buf = &primaryLogBuffer{}
		db.primaryLogs[id] = buf
	}
	buf.append(stream, body)
}

func (b *primaryLogBuffer) append(stream int64, body string) {
	if b.err != nil {
		return
	}
	if b.spillFailed || (b.file == nil && b.bytes+len(body) <= primaryLogMemoryLimit) {
		b.appendMemory(stream, body)
		return
	}
	if b.file == nil {
		file, err := os.CreateTemp("", "dagger-primary-logs-*")
		if err != nil {
			b.abandonSpill(err)
			b.appendMemory(stream, body)
			return
		}
		b.file = file
		b.cleanup = runtime.AddCleanup(b, func(file *os.File) {
			_ = file.Close()
			_ = os.Remove(file.Name())
		}, b.file)
		for i, chunk := range b.chunks {
			if err := b.write(chunk.stream, chunk.body); err != nil {
				// What was written stays in the file; the rest stays here.
				b.chunks = b.chunks[i:]
				b.abandonSpill(err)
				if b.err == nil {
					b.appendMemory(stream, body)
				}
				return
			}
		}
		b.chunks = nil
		b.bytes = 0
	}
	if err := b.write(stream, []byte(body)); err != nil {
		b.abandonSpill(err)
		if b.err == nil {
			b.appendMemory(stream, body)
		}
	}
}

func (b *primaryLogBuffer) appendMemory(stream int64, body string) {
	if n := len(b.chunks); n > 0 && b.chunks[n-1].stream == stream {
		b.chunks[n-1].body = append(b.chunks[n-1].body, body...)
	} else {
		b.chunks = append(b.chunks, primaryLogChunk{stream, []byte(body)})
	}
	b.bytes += len(body)
}

// abandonSpill keeps all further output in memory after temporary storage
// failed. A partially written record is cut off the file first; if even that
// fails, the file can no longer be replayed faithfully and the failure is
// reported instead.
func (b *primaryLogBuffer) abandonSpill(err error) {
	if b.file != nil {
		if terr := b.file.Truncate(b.fileSize); terr != nil {
			b.err = errors.Join(err, terr)
			return
		}
	}
	b.spillFailed = true
	if primarySpillWarned.CompareAndSwap(false, true) {
		// Not under primaryLogsMu: the log may be rendered by the frontend.
		go slog.Warn("cannot spill command output to temporary storage; keeping it in memory", "err", err)
	}
}

func (b *primaryLogBuffer) write(stream int64, body []byte) error {
	var header [16]byte
	binary.LittleEndian.PutUint64(header[:8], uint64(stream))
	binary.LittleEndian.PutUint64(header[8:], uint64(len(body)))
	if _, err := b.file.Write(header[:]); err != nil {
		return err
	}
	if _, err := b.file.Write(body); err != nil {
		return err
	}
	b.fileSize += int64(len(header) + len(body))
	return nil
}

// WalkPrimaryLogs replays exact bytes in original stream order without loading
// the whole command output. The callback must not retain data or call back into
// primary log storage. Replay is serialized with ingestion and cleanup.
func (db *DB) WalkPrimaryLogs(id SpanID, fn func(stream int64, data []byte) error) error {
	db.primaryLogsMu.Lock()
	defer db.primaryLogsMu.Unlock()
	b := db.primaryLogs[id]
	if b == nil {
		return nil
	}
	defer runtime.KeepAlive(b)
	if b.err != nil {
		return fmt.Errorf("buffer primary output: %w", b.err)
	}
	var scratch [32 << 10]byte
	emit := func(stream int64, r io.Reader) error {
		for {
			n, err := r.Read(scratch[:])
			if n > 0 {
				if e := fn(stream, scratch[:n]); e != nil {
					return e
				}
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
		}
	}
	emitMemory := func() error {
		for _, chunk := range b.chunks {
			if err := emit(chunk.stream, bytes.NewReader(chunk.body)); err != nil {
				return err
			}
		}
		return nil
	}
	if b.file == nil {
		return emitMemory()
	}
	stat, err := b.file.Stat()
	if err != nil {
		return err
	}
	r := io.NewSectionReader(b.file, 0, stat.Size())
	for {
		var header [16]byte
		n, err := io.ReadFull(r, header[:])
		if n == 0 && errors.Is(err, io.EOF) {
			// After a failed spill, the rest of the output is in memory.
			return emitMemory()
		}
		if err != nil {
			return fmt.Errorf("read primary output: %w", err)
		}
		stream := int64(binary.LittleEndian.Uint64(header[:8]))
		size := binary.LittleEndian.Uint64(header[8:])
		offset, _ := r.Seek(0, io.SeekCurrent)
		if size > uint64(stat.Size()-offset) {
			return io.ErrUnexpectedEOF
		}
		if err := emit(stream, io.LimitReader(r, int64(size))); err != nil {
			return err
		}
	}
}

// ClosePrimaryLogs releases temporary output storage AFTER final rendering.
// Exporter Shutdown is too early: primary output has not yet been printed then.
func (db *DB) ClosePrimaryLogs() error {
	db.primaryLogsMu.Lock()
	defer db.primaryLogsMu.Unlock()
	db.primaryLogsClosed = true
	var errs []error
	for id, b := range db.primaryLogs {
		if b.err != nil {
			errs = append(errs, b.err)
		}
		if b.file != nil {
			b.cleanup.Stop()
			errs = append(errs, b.file.Close(), os.Remove(b.file.Name()))
		}
		delete(db.primaryLogs, id)
	}
	return errors.Join(errs...)
}
