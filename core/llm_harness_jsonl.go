package core

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// DefaultLLMHarnessJSONLMaxRecordSize bounds one native protocol frame.
const DefaultLLMHarnessJSONLMaxRecordSize = 4 * 1024 * 1024

var (
	ErrLLMHarnessJSONLRecordTooLarge = errors.New("LLM harness JSONL record too large")
	ErrLLMHarnessMalformedJSONL      = errors.New("malformed LLM harness JSONL record")
)

// LLMHarnessJSONLError describes a failed JSONL record without retaining its
// potentially sensitive contents.
type LLMHarnessJSONLError struct {
	Record int64
	Size   int
	Max    int
	Err    error
}

func (e *LLMHarnessJSONLError) Error() string {
	if errors.Is(e.Err, ErrLLMHarnessJSONLRecordTooLarge) {
		return fmt.Sprintf("LLM harness JSONL record %d is %d bytes (maximum %d): %v", e.Record, e.Size, e.Max, e.Err)
	}
	return fmt.Sprintf("LLM harness JSONL record %d: %v", e.Record, e.Err)
}

func (e *LLMHarnessJSONLError) Unwrap() error {
	return e.Err
}

// LLMHarnessJSONLReader reads one bounded JSON value per line.
type LLMHarnessJSONLReader struct {
	r      *bufio.Reader
	max    int
	record int64
}

// NewLLMHarnessJSONLReader constructs a bounded framing reader. A non-positive
// maximum selects DefaultLLMHarnessJSONLMaxRecordSize.
func NewLLMHarnessJSONLReader(r io.Reader, maxRecordSize int) *LLMHarnessJSONLReader {
	maxRecordSize = llmHarnessJSONLMaxRecordSize(maxRecordSize)
	bufferSize := 64 * 1024
	if maxRecordSize < bufferSize-2 {
		bufferSize = maxRecordSize + 2
	}
	return &LLMHarnessJSONLReader{
		r:   bufio.NewReaderSize(r, bufferSize),
		max: maxRecordSize,
	}
}

// ReadRecord returns the next JSON record without its newline. The returned
// bytes are detached from the reader and safe for the caller to retain.
func (r *LLMHarnessJSONLReader) ReadRecord() (json.RawMessage, error) {
	recordNumber := r.record + 1
	var record []byte
	for {
		fragment, err := r.r.ReadSlice('\n')
		record = append(record, fragment...)
		if len(record) > 2 && len(record)-2 > r.max {
			if errors.Is(err, bufio.ErrBufferFull) {
				r.discardRecord()
			}
			r.record = recordNumber
			return nil, &LLMHarnessJSONLError{
				Record: recordNumber,
				Size:   len(record),
				Max:    r.max,
				Err:    ErrLLMHarnessJSONLRecordTooLarge,
			}
		}

		switch {
		case err == nil:
			record = record[:len(record)-1]
			if len(record) > 0 && record[len(record)-1] == '\r' {
				record = record[:len(record)-1]
			}
			return r.validateRecord(recordNumber, record)
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			if len(record) == 0 {
				return nil, io.EOF
			}
			return r.validateRecord(recordNumber, record)
		default:
			return nil, err
		}
	}
}

// Decode reads and unmarshals the next bounded record.
func (r *LLMHarnessJSONLReader) Decode(dst any) error {
	record, err := r.ReadRecord()
	if err != nil {
		return err
	}
	if err := json.Unmarshal(record, dst); err != nil {
		return &LLMHarnessJSONLError{
			Record: r.record,
			Size:   len(record),
			Max:    r.max,
			Err:    fmt.Errorf("%w: %v", ErrLLMHarnessMalformedJSONL, err),
		}
	}
	return nil
}

func (r *LLMHarnessJSONLReader) validateRecord(recordNumber int64, record []byte) (json.RawMessage, error) {
	r.record = recordNumber
	if len(record) > r.max {
		return nil, &LLMHarnessJSONLError{
			Record: recordNumber,
			Size:   len(record),
			Max:    r.max,
			Err:    ErrLLMHarnessJSONLRecordTooLarge,
		}
	}
	trimmed := bytes.TrimSpace(record)
	if len(trimmed) == 0 || bytes.ContainsAny(trimmed, "\r\n") || !json.Valid(trimmed) {
		return nil, &LLMHarnessJSONLError{
			Record: recordNumber,
			Size:   len(record),
			Max:    r.max,
			Err:    ErrLLMHarnessMalformedJSONL,
		}
	}
	return append(json.RawMessage(nil), trimmed...), nil
}

func (r *LLMHarnessJSONLReader) discardRecord() {
	for {
		_, err := r.r.ReadSlice('\n')
		if err == nil || errors.Is(err, io.EOF) {
			return
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return
		}
	}
}

// LLMHarnessJSONLWriter writes one bounded JSON value per line.
type LLMHarnessJSONLWriter struct {
	w      io.Writer
	max    int
	record int64
}

// NewLLMHarnessJSONLWriter constructs a bounded framing writer. A non-positive
// maximum selects DefaultLLMHarnessJSONLMaxRecordSize.
func NewLLMHarnessJSONLWriter(w io.Writer, maxRecordSize int) *LLMHarnessJSONLWriter {
	return &LLMHarnessJSONLWriter{
		w:   w,
		max: llmHarnessJSONLMaxRecordSize(maxRecordSize),
	}
}

// Encode marshals and writes one JSONL record.
func (w *LLMHarnessJSONLWriter) Encode(value any) error {
	record, err := json.Marshal(value)
	if err != nil {
		return &LLMHarnessJSONLError{
			Record: w.record + 1,
			Max:    w.max,
			Err:    fmt.Errorf("%w: %v", ErrLLMHarnessMalformedJSONL, err),
		}
	}
	return w.WriteRecord(record)
}

// WriteRecord validates and writes an already encoded JSON record followed by
// exactly one newline.
func (w *LLMHarnessJSONLWriter) WriteRecord(record json.RawMessage) error {
	recordNumber := w.record + 1
	trimmed := bytes.TrimSpace(record)
	if len(trimmed) == 0 || bytes.ContainsAny(trimmed, "\r\n") || !json.Valid(trimmed) {
		return &LLMHarnessJSONLError{
			Record: recordNumber,
			Size:   len(record),
			Max:    w.max,
			Err:    ErrLLMHarnessMalformedJSONL,
		}
	}
	if len(trimmed) > w.max {
		return &LLMHarnessJSONLError{
			Record: recordNumber,
			Size:   len(trimmed),
			Max:    w.max,
			Err:    ErrLLMHarnessJSONLRecordTooLarge,
		}
	}
	framed := make([]byte, len(trimmed)+1)
	copy(framed, trimmed)
	framed[len(trimmed)] = '\n'
	n, err := w.w.Write(framed)
	if err != nil {
		return err
	}
	if n != len(framed) {
		return io.ErrShortWrite
	}
	w.record = recordNumber
	return nil
}

func llmHarnessJSONLMaxRecordSize(maxRecordSize int) int {
	if maxRecordSize <= 0 {
		return DefaultLLMHarnessJSONLMaxRecordSize
	}
	return maxRecordSize
}
