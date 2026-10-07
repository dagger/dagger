package dagui

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	telemetry "github.com/dagger/otel-go"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// Primary output must survive until the final render, but retaining SDK records
// also retains their resource/attribute graphs and arbitrarily large bodies.
// Keep only a small byte prefix in memory and spill the rest, preserving stream
// order. Readers replay in bounded chunks, including for a single huge record.
const primaryLogMemoryLimit = 64 << 10

type primaryLogChunk struct {
	stream int64
	body   string
}

type primaryLogBuffer struct {
	chunks  []primaryLogChunk
	bytes   int
	file    *os.File
	cleanup runtime.Cleanup
	err     error
}

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
	if b.file == nil && b.bytes+len(body) <= primaryLogMemoryLimit && len(b.chunks) < 256 {
		b.chunks = append(b.chunks, primaryLogChunk{stream, strings.Clone(body)})
		b.bytes += len(body)
		return
	}
	if b.file == nil {
		b.file, b.err = os.CreateTemp("", "dagger-primary-logs-*")
		if b.err != nil {
			return
		}
		b.cleanup = runtime.AddCleanup(b, func(file *os.File) {
			_ = file.Close()
			_ = os.Remove(file.Name())
		}, b.file)
		for _, chunk := range b.chunks {
			if b.err = b.write(chunk.stream, chunk.body); b.err != nil {
				return
			}
		}
		b.chunks = nil
		b.bytes = 0
	}
	b.err = b.write(stream, body)
}

func (b *primaryLogBuffer) write(stream int64, body string) error {
	var header [16]byte
	binary.LittleEndian.PutUint64(header[:8], uint64(stream))
	binary.LittleEndian.PutUint64(header[8:], uint64(len(body)))
	if _, err := b.file.Write(header[:]); err != nil {
		return err
	}
	_, err := io.WriteString(b.file, body)
	return err
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
	if b.file == nil {
		for _, chunk := range b.chunks {
			if err := emit(chunk.stream, strings.NewReader(chunk.body)); err != nil {
				return err
			}
		}
		return nil
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
			return nil
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
