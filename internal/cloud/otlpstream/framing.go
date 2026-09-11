// Package otlpstream implements the binary framing Dagger Cloud's OTLP read
// streams speak — a byte-for-byte mirror of the server's api/otlpstream
// package (dagger.io#5226, "feat(cloud): stream telemetry over binary OTLP"),
// kept here because the server repository is not importable.
//
// A stream is a sequence of frames. Each frame is a 16-byte header — 4 bytes
// of magic naming the frame kind, an 8-byte big-endian cursor, a 4-byte
// big-endian payload length — followed by the payload:
//
//   - a DATA frame carries one binary-protobuf OTLP export request; a data
//     frame with an EMPTY payload is a heartbeat, keeping intermediaries from
//     closing a quiet connection;
//   - a TERMINAL frame ends the stream: every selected row has been emitted;
//   - an ERROR frame carries a UTF-8 message and ends the stream in failure.
//
// Cursors increase monotonically, and a connection that ends without a
// terminal or error frame was TRUNCATED — which is what lets a consumer tell
// "the trace is fully transferred" from "the edge dropped the connection",
// a distinction the SSE protocol this replaced could not make.
package otlpstream

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	// ContentType is the media type of a framed stream; it doubles as the
	// Accept value a consumer negotiates with.
	ContentType = "application/vnd.dagger.otlp.stream"
	// CursorHeader is the resume header the server recognizes (and, today,
	// refuses for any cursor but 0).
	CursorHeader   = "X-Dagger-Telemetry-Cursor"
	FrameHeaderLen = 16
	MaxPayloadSize = 64 << 20
)

var (
	DataMagic     = [4]byte{'D', 'T', 'P', 1}
	TerminalMagic = [4]byte{'D', 'T', 'E', 1}
	ErrorMagic    = [4]byte{'D', 'T', 'X', 1}
)

type FrameKind uint8

const (
	FrameData FrameKind = iota + 1
	FrameTerminal
	FrameError
)

type Frame struct {
	Kind    FrameKind
	Cursor  uint64
	Payload []byte
}

// ReadFrame reads one complete frame, tolerating arbitrary fragmentation of
// both the fixed-size header and payload.
func ReadFrame(r io.Reader) (Frame, error) {
	var header [FrameHeaderLen]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return Frame{}, err
	}

	kind, err := frameKind([4]byte(header[:4]))
	if err != nil {
		return Frame{}, err
	}
	size := binary.BigEndian.Uint32(header[12:])
	if size > MaxPayloadSize {
		return Frame{}, fmt.Errorf("OTLP stream payload is %d bytes (maximum %d)", size, MaxPayloadSize)
	}

	frame := Frame{
		Kind:    kind,
		Cursor:  binary.BigEndian.Uint64(header[4:12]),
		Payload: make([]byte, int(size)),
	}
	if _, err := io.ReadFull(r, frame.Payload); err != nil {
		return Frame{}, err
	}
	return frame, nil
}

func frameKind(magic [4]byte) (FrameKind, error) {
	switch magic {
	case DataMagic:
		return FrameData, nil
	case TerminalMagic:
		return FrameTerminal, nil
	case ErrorMagic:
		return FrameError, nil
	default:
		return 0, fmt.Errorf("unknown OTLP stream frame magic %q", magic[:])
	}
}

// WriteFrame writes one frame with an explicit cursor. The CLI only ever
// consumes these streams; the writing half exists for the test fakes that
// stand in for Cloud (and for tests that need to misbehave, e.g. repeat a
// cursor).
func WriteFrame(w io.Writer, kind FrameKind, cursor uint64, payload []byte) error {
	if len(payload) > MaxPayloadSize {
		return fmt.Errorf("OTLP stream payload is %d bytes (maximum %d)", len(payload), MaxPayloadSize)
	}
	var magic [4]byte
	switch kind {
	case FrameData:
		magic = DataMagic
	case FrameTerminal:
		magic = TerminalMagic
	case FrameError:
		magic = ErrorMagic
	default:
		return fmt.Errorf("unknown OTLP stream frame kind %d", kind)
	}
	var header [FrameHeaderLen]byte
	copy(header[:4], magic[:])
	binary.BigEndian.PutUint64(header[4:12], cursor)
	binary.BigEndian.PutUint32(header[12:16], uint32(len(payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// FrameWriter emits frames the way the server's live writer does: cursors
// assigned sequentially from 1, heartbeats as empty data frames.
type FrameWriter struct {
	w      io.Writer
	cursor uint64
}

func NewFrameWriter(w io.Writer) *FrameWriter { return &FrameWriter{w: w} }

func (fw *FrameWriter) WriteData(payload []byte) error { return fw.write(FrameData, payload) }
func (fw *FrameWriter) WriteHeartbeat() error          { return fw.write(FrameData, nil) }
func (fw *FrameWriter) WriteTerminal() error           { return fw.write(FrameTerminal, nil) }
func (fw *FrameWriter) WriteError(msg string) error    { return fw.write(FrameError, []byte(msg)) }

func (fw *FrameWriter) write(kind FrameKind, payload []byte) error {
	fw.cursor++
	return WriteFrame(fw.w, kind, fw.cursor, payload)
}
