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
	// LiveCursorHeader resumes a subscription after the last consumed row ID.
	LiveCursorHeader = "X-Dagger-Telemetry-Cursor"

	liveFrameHeaderSize = 16
	maxLivePayloadSize  = 64 << 20
)

var (
	liveFrameMagic    = [4]byte{'D', 'T', 'P', 1}
	liveTerminalMagic = [4]byte{'D', 'T', 'E', 1}
)

// ErrInvalidLiveFrame identifies malformed or out-of-bounds frame metadata.
var ErrInvalidLiveFrame = errors.New("invalid live telemetry frame")

// WriteLiveFrame writes one cursor-addressed protobuf payload.
func WriteLiveFrame(w io.Writer, cursor int64, payload []byte) error {
	return writeLiveFrame(w, liveFrameMagic, cursor, payload)
}

// WriteLiveTerminal writes the marker sent after the server has drained the
// subscription.
func WriteLiveTerminal(w io.Writer, cursor int64) error {
	return writeLiveFrame(w, liveTerminalMagic, cursor, nil)
}

func writeLiveFrame(w io.Writer, magic [4]byte, cursor int64, payload []byte) error {
	if cursor < 0 {
		return fmt.Errorf("%w: negative cursor %d", ErrInvalidLiveFrame, cursor)
	}
	if len(payload) > maxLivePayloadSize {
		return fmt.Errorf("%w: payload is %d bytes (maximum %d)", ErrInvalidLiveFrame, len(payload), maxLivePayloadSize)
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
	if magic != liveFrameMagic && !terminal {
		return 0, nil, false, fmt.Errorf("%w: bad magic %x", ErrInvalidLiveFrame, header[:4])
	}

	wireCursor := binary.BigEndian.Uint64(header[4:12])
	if wireCursor > uint64(^uint64(0)>>1) {
		return 0, nil, false, fmt.Errorf("%w: cursor %d overflows int64", ErrInvalidLiveFrame, wireCursor)
	}
	cursor = int64(wireCursor)

	payloadSize := binary.BigEndian.Uint32(header[12:16])
	if payloadSize > maxLivePayloadSize {
		return 0, nil, false, fmt.Errorf("%w: payload is %d bytes (maximum %d)", ErrInvalidLiveFrame, payloadSize, maxLivePayloadSize)
	}
	if terminal {
		if payloadSize != 0 {
			return 0, nil, false, fmt.Errorf("%w: terminal frame has %d-byte payload", ErrInvalidLiveFrame, payloadSize)
		}
		return cursor, nil, true, nil
	}

	payload = make([]byte, int(payloadSize))
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, false, fmt.Errorf("read live telemetry frame payload: %w", err)
	}
	return cursor, payload, false, nil
}
