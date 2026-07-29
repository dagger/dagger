package clientdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	DumpAll     = "all"
	DumpSpans   = "spans"
	DumpLogs    = "logs"
	DumpMetrics = "metrics"
)

// Dump writes the selected persisted stream rows as JSON lines. It opens files
// read-only and is intended for a closed store, whose tails have been flushed.
func Dump(ctx context.Context, root, clientID, selection string, out io.Writer) error {
	if selection == "" {
		selection = DumpAll
	}
	encoder := json.NewEncoder(out)
	for _, stream := range []string{DumpSpans, DumpLogs, DumpMetrics} {
		if selection != DumpAll && selection != stream {
			continue
		}
		path := filepath.Join(root, clientID+"."+stream+".log")
		var err error
		switch stream {
		case DumpSpans:
			err = dumpFile(ctx, path, stream, spanCodec, encoder)
		case DumpLogs:
			err = dumpFile(ctx, path, stream, logCodec, encoder)
		case DumpMetrics:
			err = dumpFile(ctx, path, stream, metricCodec, encoder)
		}
		if err != nil {
			return err
		}
	}
	if selection != DumpAll && selection != DumpSpans && selection != DumpLogs && selection != DumpMetrics {
		return fmt.Errorf("unknown telemetry stream %q", selection)
	}
	return nil
}

func dumpFile[Row any](ctx context.Context, path, stream string, codec rowCodec[Row], encoder *json.Encoder) (rerr error) {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s stream: %w", stream, err)
	}
	defer func() { rerr = errors.Join(rerr, file.Close()) }()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat %s stream: %w", stream, err)
	}
	if info.Size() < 1 {
		return fmt.Errorf("%s stream has no format header", stream)
	}
	var header [1]byte
	if _, err := file.ReadAt(header[:], 0); err != nil {
		return fmt.Errorf("read %s stream header: %w", stream, err)
	}
	if header[0] != storeFormatVersion {
		return fmt.Errorf("unsupported %s stream format version %d", stream, header[0])
	}

	scanner := newFrameScanner(file, 1, info.Size())
	for {
		frameOffset := scanner.offset
		payload, err := scanner.next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read %s stream frame at %d: %w", stream, frameOffset, err)
		}
		row, err := codec.decode(payload)
		if err != nil {
			return fmt.Errorf("decode %s stream frame at %d: %w", stream, frameOffset, err)
		}
		if err := encoder.Encode(struct {
			Stream string `json:"stream"`
			Row    Row    `json:"row"`
		}{Stream: stream, Row: row}); err != nil {
			return fmt.Errorf("encode %s row %d: %w", stream, codec.getID(row), err)
		}
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		default:
		}
	}
}
