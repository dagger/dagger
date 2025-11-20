package util

import (
	"context"
	"encoding/binary"
	"io"
	"sync"

	"github.com/dagger/dagger/internal/fsutil"
)

var bufPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 32*1<<10)
		return &buf
	},
}

func NewProtoStream(ctx context.Context, r io.Reader, w io.Writer) fsutil.Stream {
	return &protoStream{ctx, r, w}
}

type protoStream struct {
	ctx context.Context
	io.Reader
	io.Writer
}

func (c *protoStream) RecvMsg(m interface{}) error {
	type unmarshaler interface {
		Unmarshal([]byte) error
	}
	var h [4]byte
	if _, err := io.ReadFull(c.Reader, h[:]); err != nil {
		return err
	}
	msg := m.(unmarshaler)
	length := binary.BigEndian.Uint32(h[:])
	if length == 0 {
		return nil
	}
	buf := *bufPool.Get().(*[]byte)
	if cap(buf) < int(length) {
		buf = make([]byte, length)
	} else {
		buf = buf[:length]
	}
	defer bufPool.Put(&buf)
	if _, err := io.ReadFull(c.Reader, buf); err != nil {
		return err
	}
	err := msg.Unmarshal(buf)
	if err != nil {
		return err
	}
	return nil
}

func (c *protoStream) SendMsg(m interface{}) error {
	type marshalerSizer interface {
		MarshalTo([]byte) (int, error)
		Size() int
	}
	msg := m.(marshalerSizer)
	size := msg.Size()
	b := make([]byte, msg.Size()+4)
	binary.BigEndian.PutUint32(b[:4], uint32(size))
	if _, err := msg.MarshalTo(b[4:]); err != nil {
		return err
	}
	_, err := c.Writer.Write(b)
	return err
}

func (c *protoStream) Context() context.Context {
	return c.ctx
}
