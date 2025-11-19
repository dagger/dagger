package bench

import (
	"context"

	"github.com/pkg/errors"
	"github.com/tonistiigi/fsutil"
	fstypes "github.com/tonistiigi/fsutil/types"
	"golang.org/x/sync/errgroup"
)

func diffCopy(proto bool, src, dest string) error {
	var s1, s2 fsutil.Stream

	eg, ctx := errgroup.WithContext(context.Background())

	if proto {
		s1, s2 = sockPairProto(ctx)
	} else {
		s1, s2 = sockPair(ctx)
	}

	eg.Go(func() error {
		return fsutil.Send(ctx, s1, fsutil.NewFS(src, nil), nil)
	})
	eg.Go(func() error {
		return fsutil.Receive(ctx, s2, dest, fsutil.ReceiveOpt{})
	})

	return eg.Wait()
}

func diffCopyProto(src, dest string) error {
	return diffCopy(true, src, dest)
}
func diffCopyReg(src, dest string) error {
	return diffCopy(false, src, dest)
}

func sockPair(ctx context.Context) (fsutil.Stream, fsutil.Stream) {
	c1 := make(chan *fstypes.Packet, 64)
	c2 := make(chan *fstypes.Packet, 64)
	return &fakeConn{ctx, c1, c2}, &fakeConn{ctx, c2, c1}
}

func sockPairProto(ctx context.Context) (fsutil.Stream, fsutil.Stream) {
	c1 := make(chan []byte, 64)
	c2 := make(chan []byte, 64)
	return &fakeConnProto{ctx, c1, c2}, &fakeConnProto{ctx, c2, c1}
}

type fakeConn struct {
	ctx      context.Context
	recvChan chan *fstypes.Packet
	sendChan chan *fstypes.Packet
}

func (fc *fakeConn) Context() context.Context {
	return fc.ctx
}

func (fc *fakeConn) RecvMsg(m interface{}) error {
	p, ok := m.(*fstypes.Packet)
	if !ok {
		return errors.Errorf("invalid msg: %#v", m)
	}
	select {
	case <-fc.ctx.Done():
		return fc.ctx.Err()
	case p2 := <-fc.recvChan:
		*p = *p2
		return nil
	}
}

func (fc *fakeConn) SendMsg(m interface{}) error {
	p, ok := m.(*fstypes.Packet)
	if !ok {
		return errors.Errorf("invalid msg: %#v", m)
	}
	p2 := *p
	p2.Data = append([]byte{}, p2.Data...)
	select {
	case <-fc.ctx.Done():
		return fc.ctx.Err()
	case fc.sendChan <- &p2:
		return nil
	}
}

type fakeConnProto struct {
	ctx      context.Context
	recvChan chan []byte
	sendChan chan []byte
}

func (fc *fakeConnProto) Context() context.Context {
	return fc.ctx
}

func (fc *fakeConnProto) RecvMsg(m interface{}) error {
	p, ok := m.(*fstypes.Packet)
	if !ok {
		return errors.Errorf("invalid msg: %#v", m)
	}
	select {
	case <-fc.ctx.Done():
		return fc.ctx.Err()
	case dt := <-fc.recvChan:
		return p.Unmarshal(dt)
	}
}

func (fc *fakeConnProto) SendMsg(m interface{}) error {
	p, ok := m.(*fstypes.Packet)
	if !ok {
		return errors.Errorf("invalid msg: %#v", m)
	}
	dt, err := p.Marshal()
	if err != nil {
		return err
	}
	select {
	case <-fc.ctx.Done():
		return fc.ctx.Err()
	case fc.sendChan <- dt:
		return nil
	}
}
