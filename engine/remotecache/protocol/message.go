package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"weak"

	"github.com/klauspost/compress/zstd"
)

// The protocol compresses its own messages; the WebSocket's compression is
// off. An envelope's JSON of at most CompressAbove bytes goes as it is, as a
// text message. A larger one goes compressed, as a binary message: the
// JSON's length as a uvarint, then the JSON compressed as one zstd frame. So
// a message is compressed as it is encoded, before the connection's writer
// is taken, which then only writes it; the JSON is never in memory whole on
// the writing side; and a reader learns the JSON's length, which it checks,
// before it decompresses anything.
//
// The JSON's length is what the message limit, MaxMessageBytes, bounds, on
// both sides: a writer stops encoding a message over it (EncodeMessage), and
// a reader refuses one that declares more, or decompresses to another
// length than it declares (DecodeMessage). A message cut short never
// decodes: a zstd frame cut short fails to decompress, and JSON cut short
// fails to parse.

// CompressAbove is the length of an envelope's JSON above which the protocol
// sends it compressed: the WebSocket library's own threshold for
// compression without context takeover, so a message small enough to go
// uncompressed before still does.
const CompressAbove = 512

// ErrTooLarge fails a message whose JSON is over the message limit, which
// is not sent.
var ErrTooLarge = errors.New(TooLargeMessage)

// Message is an envelope as the protocol sends it (EncodeMessage): Data, the
// JSON itself, or compressed, its length and its zstd frame (Compressed); and
// Size, the JSON's length.
type Message struct {
	Data       []byte
	Compressed bool
	Size       int
}

// EncodeMessage encodes the envelope of body as the protocol sends it: the
// JSON EncodeEnvelope returns, compressed as it is encoded once it is over
// CompressAbove bytes. A message over limit bytes of JSON stops encoding
// there and fails with ErrTooLarge.
func EncodeMessage(id, re uint64, typ Type, body any, limit int) (Message, error) {
	w := &messageWriter{limit: limit}
	err := WriteEnvelope(w, id, re, typ, body)
	return w.finish(err)
}

// messageWriter takes a message's JSON as it is encoded: its first
// CompressAbove bytes as they are, and once the JSON is longer, the whole of
// it into a zstd encoder writing into out, after room for the JSON's length.
type messageWriter struct {
	limit int
	n     int
	plain []byte
	out   *bytes.Buffer
	enc   *zstd.Encoder
}

func (w *messageWriter) Write(p []byte) (int, error) {
	if w.n+len(p) > w.limit {
		return 0, ErrTooLarge
	}
	w.n += len(p)
	if w.enc == nil && w.n <= CompressAbove {
		w.plain = append(w.plain, p...)
		return len(p), nil
	}
	if w.enc == nil {
		w.out = &bytes.Buffer{}
		w.out.Write(make([]byte, binary.MaxVarintLen64))
		enc, err := messageEncoders.get()
		if err != nil {
			return 0, err
		}
		w.enc = enc
		w.enc.Reset(w.out)
		if _, err := w.enc.Write(w.plain); err != nil {
			return 0, err
		}
		w.plain = nil
	}
	return w.enc.Write(p)
}

// finish ends the message, after an encoding that returned err: an encoder
// it took goes back to its pool, ended or not.
func (w *messageWriter) finish(err error) (Message, error) {
	if w.enc != nil {
		if err == nil {
			err = w.enc.Close()
		}
		w.enc.Reset(nil)
		messageEncoders.put(w.enc)
		w.enc = nil
	}
	if err != nil {
		return Message{}, err
	}
	if w.out == nil {
		return Message{Data: w.plain, Size: w.n}, nil
	}
	// The JSON's length goes in the room left for it, just before the frame.
	data := w.out.Bytes()
	var length [binary.MaxVarintLen64]byte
	k := binary.PutUvarint(length[:], uint64(w.n))
	start := binary.MaxVarintLen64 - k
	copy(data[start:], length[:k])
	return Message{Data: data[start:], Compressed: true, Size: w.n}, nil
}

// messageEncoders keeps idle the zstd encoders that compress messages, each
// taken by one message at a time: an encoder holds about 3 MB, a history of
// twice its window at the fastest level, and making one costs as much. It
// keeps at most as many as can work at once, as compressing takes a core,
// and by weak pointers, so a collection takes those idle meanwhile: a
// sync.Pool keeps one in each core's slot and the generation before the
// last collection, several times that.
var messageEncoders = newIdleEncoders(runtime.GOMAXPROCS(0))

// idleEncoders are encoders kept idle between messages, at most as many as
// its channel holds.
type idleEncoders struct {
	idle chan weak.Pointer[zstd.Encoder]
}

func newIdleEncoders(keep int) *idleEncoders {
	return &idleEncoders{idle: make(chan weak.Pointer[zstd.Encoder], keep)}
}

// get takes an idle encoder, or makes one when none is left.
func (e *idleEncoders) get() (*zstd.Encoder, error) {
	for {
		select {
		case idle := <-e.idle:
			if enc := idle.Value(); enc != nil {
				return enc, nil
			}
		default:
			return zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest), zstd.WithEncoderConcurrency(1), zstd.WithWindowSize(1<<20))
		}
	}
}

// put keeps enc idle, unless as many as it keeps are idle already.
func (e *idleEncoders) put(enc *zstd.Encoder) {
	select {
	case e.idle <- weak.Make(enc):
	default:
	}
}

// messageSize returns the length of a message's JSON, as the message gives
// it, without decompressing it: a compressed message's declared length,
// refused over limit.
func messageSize(data []byte, compressed bool, limit int) (int, error) {
	if !compressed {
		if len(data) > limit {
			return 0, fmt.Errorf("message of %d bytes: %w", len(data), ErrTooLarge)
		}
		return len(data), nil
	}
	size, k := binary.Uvarint(data)
	if k <= 0 {
		return 0, errors.New("compressed message: no length")
	}
	if size > uint64(limit) {
		return 0, fmt.Errorf("compressed message of %d bytes: %w", size, ErrTooLarge)
	}
	return int(size), nil
}

// DecodeMessage returns a message's JSON: the message itself, or a
// compressed message's frame decompressed, which must be the length it
// declares; a message over limit bytes of JSON, as it declares them, is
// refused before anything is decompressed.
func DecodeMessage(data []byte, compressed bool, limit int) ([]byte, error) {
	size, err := messageSize(data, compressed, limit)
	if err != nil || !compressed {
		return data, err
	}
	dec, err := messageDecoder()
	if err != nil {
		return nil, err
	}
	_, k := binary.Uvarint(data)
	json, err := dec.DecodeAll(data[k:], make([]byte, 0, size))
	if err != nil {
		return nil, fmt.Errorf("compressed message: %w", err)
	}
	if len(json) != size {
		return nil, fmt.Errorf("compressed message: %d bytes, and it declares %d", len(json), size)
	}
	return json, nil
}

// messageDecoder is the zstd decoder of compressed messages: it decodes as
// many at once as there are cores, each with the history of the window a
// message's frame is written with, into memory of at most the message
// limit.
var messageDecoder = sync.OnceValues(func() (*zstd.Decoder, error) {
	return zstd.NewReader(nil, zstd.WithDecoderConcurrency(0), zstd.WithDecoderMaxWindow(1<<20), zstd.WithDecoderMaxMemory(MaxMessageBytes))
})
