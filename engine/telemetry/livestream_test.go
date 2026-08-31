package telemetry

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLiveFrameRoundTrip(t *testing.T) {
	t.Parallel()

	var stream bytes.Buffer
	require.NoError(t, WriteLiveFrame(&stream, 42, []byte("protobuf")))
	require.NoError(t, WriteLiveFrame(&stream, 43, nil))
	require.NoError(t, WriteLiveTerminal(&stream, 43))

	cursor, payload, terminal, err := ReadLiveFrame(&oneByteReader{Reader: &stream})
	require.NoError(t, err)
	require.Equal(t, int64(42), cursor)
	require.Equal(t, []byte("protobuf"), payload)
	require.False(t, terminal)

	cursor, payload, terminal, err = ReadLiveFrame(&oneByteReader{Reader: &stream})
	require.NoError(t, err)
	require.Equal(t, int64(43), cursor)
	require.Empty(t, payload)
	require.False(t, terminal)

	cursor, payload, terminal, err = ReadLiveFrame(&oneByteReader{Reader: &stream})
	require.NoError(t, err)
	require.Equal(t, int64(43), cursor)
	require.Nil(t, payload)
	require.True(t, terminal)
}

func TestLiveErrorRoundTrip(t *testing.T) {
	t.Parallel()

	var stream bytes.Buffer
	require.NoError(t, WriteLiveError(&stream, 42, errors.New("row too large")))

	cursor, payload, terminal, err := ReadLiveFrame(&stream)
	require.ErrorIs(t, err, ErrLiveStream)
	require.ErrorContains(t, err, "row too large")
	require.Equal(t, int64(42), cursor)
	require.Nil(t, payload)
	require.False(t, terminal)
}

func TestLiveFrameBoundsChecks(t *testing.T) {
	t.Parallel()

	t.Run("negative write cursor", func(t *testing.T) {
		err := WriteLiveFrame(io.Discard, -1, []byte("payload"))
		require.ErrorIs(t, err, ErrInvalidLiveFrame)
	})

	t.Run("bad magic", func(t *testing.T) {
		frame := make([]byte, liveFrameHeaderSize)
		_, _, _, err := ReadLiveFrame(bytes.NewReader(frame))
		require.ErrorIs(t, err, ErrInvalidLiveFrame)
	})

	t.Run("cursor overflow", func(t *testing.T) {
		frame := make([]byte, liveFrameHeaderSize)
		copy(frame[:4], liveFrameMagic[:])
		binary.BigEndian.PutUint64(frame[4:12], ^uint64(0))
		_, _, _, err := ReadLiveFrame(bytes.NewReader(frame))
		require.ErrorIs(t, err, ErrInvalidLiveFrame)
	})

	t.Run("oversized read payload", func(t *testing.T) {
		frame := make([]byte, liveFrameHeaderSize)
		copy(frame[:4], liveFrameMagic[:])
		binary.BigEndian.PutUint32(frame[12:16], MaxLivePayloadSize+1)
		_, _, _, err := ReadLiveFrame(bytes.NewReader(frame))
		require.ErrorIs(t, err, ErrInvalidLiveFrame)
	})

	t.Run("terminal payload", func(t *testing.T) {
		frame := make([]byte, liveFrameHeaderSize)
		copy(frame[:4], liveTerminalMagic[:])
		binary.BigEndian.PutUint32(frame[12:16], 1)
		_, _, _, err := ReadLiveFrame(bytes.NewReader(frame))
		require.ErrorIs(t, err, ErrInvalidLiveFrame)
	})

	t.Run("truncated payload", func(t *testing.T) {
		var frame bytes.Buffer
		require.NoError(t, WriteLiveFrame(&frame, 1, []byte("payload")))
		data := frame.Bytes()[:frame.Len()-1]
		_, _, _, err := ReadLiveFrame(bytes.NewReader(data))
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
		// A truncated transport frame is reconnectable, not a malformed header.
		require.False(t, errors.Is(err, ErrInvalidLiveFrame))
	})
}

type oneByteReader struct {
	io.Reader
}

func (r *oneByteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.Reader.Read(p)
}
