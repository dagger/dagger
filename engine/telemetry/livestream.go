package telemetry

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	// LiveContentType identifies the framed binary OTLP subscription protocol.
	LiveContentType = "application/vnd.dagger.otlp.stream"
	// LegacyLiveContentType identifies the original SSE OTLP subscription protocol.
	LegacyLiveContentType = "text/event-stream"
	// LiveCursorHeader resumes a binary subscription after the last consumed row ID.
	LiveCursorHeader = "X-Dagger-Telemetry-Cursor"
	// LegacyLiveCursorHeader resumes an SSE subscription after the last event ID.
	LegacyLiveCursorHeader = "X-Last-Event-ID"

	liveFrameHeaderSize = 16
	// MaxLivePayloadSize bounds each protobuf frame while allowing a stream to
	// carry any number of frames.
	MaxLivePayloadSize = 64 << 20
	maxLiveErrorSize   = 4 << 10
)

var (
	liveFrameMagic    = [4]byte{'D', 'T', 'P', 1}
	liveTerminalMagic = [4]byte{'D', 'T', 'E', 1}
	liveErrorMagic    = [4]byte{'D', 'T', 'X', 1}
)

var (
	// ErrInvalidLiveFrame identifies malformed or out-of-bounds frame metadata.
	ErrInvalidLiveFrame = errors.New("invalid live telemetry frame")
	// ErrLiveStream identifies an error reported by the stream producer. It is
	// not a transport interruption and must not be retried at the same cursor.
	ErrLiveStream = errors.New("live telemetry stream error")
)

// WriteLiveFrame writes one cursor-addressed protobuf payload.
func WriteLiveFrame(w io.Writer, cursor int64, payload []byte) error {
	return writeLiveFrame(w, liveFrameMagic, cursor, payload)
}

// WriteLiveTerminal writes the marker sent after the server has drained the
// subscription.
func WriteLiveTerminal(w io.Writer, cursor int64) error {
	return writeLiveFrame(w, liveTerminalMagic, cursor, nil)
}

// WriteLiveError reports a producer error that a consumer must not retry at the
// same cursor. Error text is truncated to keep error frames tightly bounded.
func WriteLiveError(w io.Writer, cursor int64, streamErr error) error {
	if streamErr == nil {
		return fmt.Errorf("%w: nil stream error", ErrInvalidLiveFrame)
	}
	message := []byte(streamErr.Error())
	if len(message) > maxLiveErrorSize {
		message = message[:maxLiveErrorSize]
	}
	return writeLiveFrame(w, liveErrorMagic, cursor, message)
}

func writeLiveFrame(w io.Writer, magic [4]byte, cursor int64, payload []byte) error {
	if cursor < 0 {
		return fmt.Errorf("%w: negative cursor %d", ErrInvalidLiveFrame, cursor)
	}
	if len(payload) > MaxLivePayloadSize {
		return fmt.Errorf("%w: payload is %d bytes (maximum %d)", ErrInvalidLiveFrame, len(payload), MaxLivePayloadSize)
	}

	var header [liveFrameHeaderSize]byte
	copy(header[:4], magic[:])
	binary.BigEndian.PutUint64(header[4:12], uint64(cursor))
	binary.BigEndian.PutUint32(header[12:16], uint32(len(payload)))
	if err := writeLiveFramePart(w, header[:]); err != nil {
		return fmt.Errorf("write live telemetry frame header: %w", err)
	}
	if len(payload) > 0 {
		if err := writeLiveFramePart(w, payload); err != nil {
			return fmt.Errorf("write live telemetry frame payload: %w", err)
		}
	}
	return nil
}

func writeLiveFramePart(w io.Writer, data []byte) error {
	n, err := w.Write(data)
	if err != nil {
		return err
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return nil
}

// ReadLiveFrame reads one cursor-addressed protobuf payload. terminal is true
// for the empty frame that marks a cleanly drained subscription.
func ReadLiveFrame(r io.Reader) (cursor int64, payload []byte, terminal bool, err error) {
	var header [liveFrameHeaderSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, false, fmt.Errorf("read live telemetry frame header: %w", err)
	}
	magic := [4]byte(header[:4])
	terminal = magic == liveTerminalMagic
	streamError := magic == liveErrorMagic
	if magic != liveFrameMagic && !terminal && !streamError {
		return 0, nil, false, fmt.Errorf("%w: bad magic %x", ErrInvalidLiveFrame, header[:4])
	}

	wireCursor := binary.BigEndian.Uint64(header[4:12])
	if wireCursor > uint64(^uint64(0)>>1) {
		return 0, nil, false, fmt.Errorf("%w: cursor %d overflows int64", ErrInvalidLiveFrame, wireCursor)
	}
	cursor = int64(wireCursor)

	payloadSize := binary.BigEndian.Uint32(header[12:16])
	if payloadSize > MaxLivePayloadSize {
		return 0, nil, false, fmt.Errorf("%w: payload is %d bytes (maximum %d)", ErrInvalidLiveFrame, payloadSize, MaxLivePayloadSize)
	}
	if terminal {
		if payloadSize != 0 {
			return 0, nil, false, fmt.Errorf("%w: terminal frame has %d-byte payload", ErrInvalidLiveFrame, payloadSize)
		}
		return cursor, nil, true, nil
	}
	if streamError && payloadSize > maxLiveErrorSize {
		return 0, nil, false, fmt.Errorf("%w: error payload is %d bytes (maximum %d)", ErrInvalidLiveFrame, payloadSize, maxLiveErrorSize)
	}

	payload = make([]byte, int(payloadSize))
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, false, fmt.Errorf("read live telemetry frame payload: %w", err)
	}
	if streamError {
		return cursor, nil, false, fmt.Errorf("%w: %s", ErrLiveStream, payload)
	}
	return cursor, payload, false, nil
}
