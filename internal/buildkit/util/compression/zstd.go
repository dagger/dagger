package compression

import (
	"context"
	"io"
	"sync"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/klauspost/compress/zstd"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func (c zstdType) Compress(ctx context.Context, comp Config) (compressorFunc Compressor, finalize Finalizer) {
	return func(dest io.Writer, _ string) (io.WriteCloser, error) {
		return zstdWriter(comp)(dest)
	}, nil
}

func (c zstdType) Decompress(ctx context.Context, cs content.Store, desc ocispecs.Descriptor) (io.ReadCloser, error) {
	return decompress(ctx, cs, desc)
}

func (c zstdType) NeedsConversion(ctx context.Context, cs content.Store, desc ocispecs.Descriptor) (bool, error) {
	if !images.IsLayerType(desc.MediaType) {
		return false, nil
	}
	ct, err := FromMediaType(desc.MediaType)
	if err != nil {
		return false, err
	}
	if ct == Zstd {
		return false, nil
	}
	return true, nil
}

func (c zstdType) NeedsComputeDiffBySelf(comp Config) bool {
	return true
}

func (c zstdType) OnlySupportOCITypes() bool {
	return false
}

func (c zstdType) MediaType() string {
	return ocispecs.MediaTypeImageLayerZstd
}

func (c zstdType) String() string {
	return "zstd"
}

func zstdWriter(comp Config) func(io.Writer) (io.WriteCloser, error) {
	return func(dest io.Writer) (io.WriteCloser, error) {
		level := zstd.SpeedDefault
		if comp.Level != nil {
			level = toZstdEncoderLevel(*comp.Level)
		}
		encoders := zstdEncodersFor(level)
		enc, _ := encoders.Get().(*zstd.Encoder)
		if enc == nil {
			// Each encoder of the pool keeps its history once used: a
			// layer's frames rotate through as many as compress at once,
			// and no more.
			var err error
			if enc, err = zstd.NewWriter(nil, zstd.WithEncoderLevel(level), zstd.WithEncoderConcurrency(zstdFramesAtOnce)); err != nil {
				return nil, err
			}
		}
		return &zstdLayerWriter{enc: enc, encoders: encoders, dest: dest}, nil
	}
}

// zstdEncoders keeps encoders between layers, by level. An encoder
// keeps its tables, its history and its blocks' buffers once used, and
// each frame or stream it starts resets them as a new encoder would, so a
// layer's blob doesn't depend on the layers its encoder compressed before.
// An engine exporting to the remote cache compresses thousands of layers,
// and a new encoder for each allocated tens of megabytes. A pool, not a
// fixed set: those the collector finds idle it frees.
var zstdEncoders sync.Map

func zstdEncodersFor(level zstd.EncoderLevel) *sync.Pool {
	if p, ok := zstdEncoders.Load(level); ok {
		return p.(*sync.Pool)
	}
	p, _ := zstdEncoders.LoadOrStore(level, &sync.Pool{})
	return p.(*sync.Pool)
}

// zstdBuffers keeps a layer writer's buffers of zstdFrameSize between
// layers: the layer held back, and each frame the writer fills.
var zstdBuffers = sync.Pool{New: func() any {
	b := make([]byte, 0, zstdFrameSize)
	return &b
}}

func getZstdBuffer() []byte { return (*zstdBuffers.Get().(*[]byte))[:0] }

func putZstdBuffer(b []byte) {
	if cap(b) < zstdFrameSize {
		return
	}
	b = b[:0]
	zstdBuffers.Put(&b)
}

// zstdOutputs keeps the buffers a layer's frames are compressed into,
// between frames: each goes back once its frame is written.
var zstdOutputs sync.Pool

func getZstdOutput() []byte {
	if b, ok := zstdOutputs.Get().(*[]byte); ok {
		return (*b)[:0]
	}
	return make([]byte, 0, zstdFrameSize/2)
}

func putZstdOutput(b []byte) {
	b = b[:0]
	zstdOutputs.Put(&b)
}

// zstdSizedLimit is the largest layer a zstdLayerWriter holds back to write
// with its size: zstd's usual window, which no larger layer can fit anyway.
const zstdSizedLimit = 8 << 20

// zstdFrameSize is how much of a larger layer each of its frames holds: the
// layer is cut into frames of this size, the last one shorter, each
// compressed on its own. The cut depends on the layer's bytes alone, so its
// blob is the same on every machine, and each frame names its size, as a
// layer that fits the window does.
const zstdFrameSize = zstdSizedLimit

// zstdFramesAtOnce is how many of a layer's frames compress at once. Their
// order in the blob is the layer's, however many run.
const zstdFramesAtOnce = 4

// zstdLayerWriter compresses a layer into one zstd frame that names the
// layer's size whenever the layer fits in zstdSizedLimit. A stream writer
// knows the size only of a layer that fits in one 128 KB block; any other
// frame declares the encoder's whole window instead, and every decoder of
// it reserves twice that, 16 MB, however small the layer. So the writer
// holds the layer back until it outgrows the limit or ends: a layer that
// ends first is written with its size, and its frame's window fits it. A
// layer that outgrows it is frames of zstdFrameSize, up to zstdFramesAtOnce
// of them compressing at once: one stream would compress on one core, and
// declare the encoder's whole window. Every zstd reader reads a blob of
// several frames as it reads one.
type zstdLayerWriter struct {
	enc *zstd.Encoder
	// encoders is where enc goes back once the layer is written.
	encoders *sync.Pool
	dest     io.Writer
	// streamed is set once enc writes the layer as a stream: a layer that
	// fits in zstdSizedLimit, written with its size.
	streamed bool
	// held is the layer so far, while it fits in zstdSizedLimit.
	held []byte
	// outgrown is set once the layer outgrows zstdSizedLimit, or ends.
	// Once it outgrows it, frame is the frame being filled, and
	// compressing the frames compressing, in the layer's order.
	outgrown    bool
	frame       []byte
	compressing []chan []byte
	// closed is set by the first Close, and closeErr is what it returned:
	// callers may Close again, as a deferred Close after an explicit one,
	// and get the same answer, with enc given back once.
	closed   bool
	closeErr error
}

func (w *zstdLayerWriter) Write(p []byte) (int, error) {
	if !w.outgrown {
		if len(w.held)+len(p) <= zstdSizedLimit {
			if w.held == nil {
				w.held = getZstdBuffer()
			}
			w.held = append(w.held, p...)
			return len(p), nil
		}
		w.outgrown = true
		w.frame = w.held
		w.held = nil
		if w.frame == nil {
			w.frame = getZstdBuffer()
		}
	}
	n := len(p)
	for len(p) > 0 {
		take := min(zstdFrameSize-len(w.frame), len(p))
		w.frame = append(w.frame, p[:take]...)
		p = p[take:]
		if len(w.frame) == zstdFrameSize {
			if err := w.compressFrame(); err != nil {
				return 0, err
			}
		}
	}
	return n, nil
}

// compressFrame starts compressing the frame filled so far, once fewer than
// zstdFramesAtOnce are: it writes the oldest out first, when it must wait.
func (w *zstdLayerWriter) compressFrame() error {
	if len(w.compressing) == zstdFramesAtOnce {
		if err := w.writeOldest(); err != nil {
			return err
		}
	}
	src := w.frame
	w.frame = getZstdBuffer()
	done := make(chan []byte, 1)
	go func() {
		out := w.enc.EncodeAll(src, getZstdOutput())
		putZstdBuffer(src)
		done <- out
	}()
	w.compressing = append(w.compressing, done)
	return nil
}

func (w *zstdLayerWriter) writeOldest() error {
	frame := <-w.compressing[0]
	w.compressing = w.compressing[1:]
	_, err := w.dest.Write(frame)
	// A writer doesn't keep what it's given.
	putZstdOutput(frame)
	return err
}

func (w *zstdLayerWriter) Close() error {
	if !w.closed {
		w.closed = true
		w.closeErr = w.close()
	}
	return w.closeErr
}

func (w *zstdLayerWriter) close() error {
	if err := w.finish(); err != nil {
		return err
	}
	// The layer is written: its encoder can compress another. One that
	// failed stays out, in whatever state it's in. One that streamed lets
	// go of the layer's destination; resetting one that compressed frames
	// would make it a stream's state it hasn't needed.
	if w.streamed {
		w.enc.Reset(nil)
	}
	w.encoders.Put(w.enc)
	w.enc = nil
	return nil
}

func (w *zstdLayerWriter) finish() error {
	if !w.outgrown {
		w.outgrown = true
		w.streamed = true
		w.enc.ResetContentSize(w.dest, int64(len(w.held)))
		held := w.held
		w.held = nil
		if _, err := w.enc.Write(held); err != nil {
			return err
		}
		err := w.enc.Close()
		putZstdBuffer(held)
		return err
	}
	if len(w.frame) > 0 {
		if err := w.compressFrame(); err != nil {
			return err
		}
	}
	for len(w.compressing) > 0 {
		if err := w.writeOldest(); err != nil {
			return err
		}
	}
	putZstdBuffer(w.frame)
	w.frame = nil
	return nil
}

func toZstdEncoderLevel(level int) zstd.EncoderLevel {
	// map zstd compression levels to go-zstd levels
	// once we also have c based implementation move this to helper pkg
	if level < 0 {
		return zstd.SpeedDefault
	} else if level < 3 {
		return zstd.SpeedFastest
	} else if level < 7 {
		return zstd.SpeedDefault
	} else if level < 9 {
		return zstd.SpeedBetterCompression
	}
	return zstd.SpeedBestCompression
}
