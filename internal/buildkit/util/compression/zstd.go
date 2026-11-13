package compression

import (
	"context"
	"io"

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
		opts := []zstd.EOption{zstd.WithEncoderLevel(level)}
		if comp.Framed {
			// Each encoder of the pool keeps its history once used: a
			// framed writer's frames rotate through as many as compress at
			// once, and no more.
			opts = append(opts, zstd.WithEncoderConcurrency(zstdFramesAtOnce))
		}
		enc, err := zstd.NewWriter(nil, opts...)
		if err != nil {
			return nil, err
		}
		return &zstdLayerWriter{enc: enc, dest: dest, framed: comp.Framed}, nil
	}
}

// zstdSizedLimit is the largest layer a zstdLayerWriter holds back to write
// with its size: zstd's usual window, which no larger layer can fit anyway.
const zstdSizedLimit = 8 << 20

// zstdFrameSize is how much of a larger layer each of its frames holds,
// when its writer is framed: the layer is cut into frames of this size, the
// last one shorter, each compressed on its own. The cut depends on the layer's bytes alone, so its
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
// layer that outgrows it is one stream, or, for a framed writer
// (Config.Framed), frames of zstdFrameSize, up to zstdFramesAtOnce of them
// compressing at once: one stream compresses on one core, which a large
// layer's export waited for.
type zstdLayerWriter struct {
	enc  *zstd.Encoder
	dest io.Writer
	// framed writes a layer that outgrows zstdSizedLimit as frames.
	framed bool
	// held is the layer so far, while it fits in zstdSizedLimit.
	held []byte
	// outgrown is set once the layer outgrows zstdSizedLimit, or ends.
	// For a framed writer, frame is then the frame being filled, and
	// compressing the frames compressing, in the layer's order.
	outgrown    bool
	frame       []byte
	compressing []chan []byte
}

func (w *zstdLayerWriter) Write(p []byte) (int, error) {
	if !w.outgrown {
		if len(w.held)+len(p) <= zstdSizedLimit {
			w.held = append(w.held, p...)
			return len(p), nil
		}
		w.outgrown = true
		held := w.held
		w.held = nil
		if !w.framed {
			// Too large to name its size: the frame declares the window.
			w.enc.Reset(w.dest)
			if _, err := w.enc.Write(held); err != nil {
				return 0, err
			}
		} else {
			w.frame = held
		}
	}
	if !w.framed {
		return w.enc.Write(p)
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
	w.frame = make([]byte, 0, zstdFrameSize)
	done := make(chan []byte, 1)
	go func() { done <- w.enc.EncodeAll(src, make([]byte, 0, len(src)/2)) }()
	w.compressing = append(w.compressing, done)
	return nil
}

func (w *zstdLayerWriter) writeOldest() error {
	frame := <-w.compressing[0]
	w.compressing = w.compressing[1:]
	_, err := w.dest.Write(frame)
	return err
}

func (w *zstdLayerWriter) Close() error {
	if !w.outgrown {
		w.outgrown = true
		w.enc.ResetContentSize(w.dest, int64(len(w.held)))
		held := w.held
		w.held = nil
		if _, err := w.enc.Write(held); err != nil {
			return err
		}
		return w.enc.Close()
	}
	if !w.framed {
		return w.enc.Close()
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
