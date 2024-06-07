package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

// converts a pre-existing net.Conn into a net.Listener that returns the conn and then blocks
type singleConnListener struct {
	conn      net.Conn
	l         sync.Mutex
	closeCh   chan struct{}
	closeOnce sync.Once
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	l.l.Lock()
	if l.conn == nil {
		l.l.Unlock()
		<-l.closeCh
		return nil, io.ErrClosedPipe
	}
	defer l.l.Unlock()

	c := l.conn
	l.conn = nil
	return c, nil
}

func (l *singleConnListener) Addr() net.Addr {
	return nil
}

func (l *singleConnListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.closeCh)
	})
	return nil
}

type nopCloserConn struct {
	net.Conn
}

func (nopCloserConn) Close() error {
	return nil
}

// TODO: could also implement this upstream on:
// https://github.com/sipsma/buildkit/blob/fa11bf9e57a68e3b5252386fdf44042dd672949a/session/grpchijack/dial.go#L45-L45
type withDeadlineConn struct {
	conn          net.Conn
	readDeadline  time.Time
	readers       []func()
	readBuf       *bytes.Buffer
	readEOF       bool
	readCond      *sync.Cond
	writeDeadline time.Time
	writers       []func()
	writersL      sync.Mutex
}

func newLogicalDeadlineConn(inner net.Conn) net.Conn {
	c := &withDeadlineConn{
		conn:     inner,
		readBuf:  new(bytes.Buffer),
		readCond: sync.NewCond(new(sync.Mutex)),
	}

	go func() {
		for {
			buf := make([]byte, 32*1024)
			n, err := inner.Read(buf)
			if err != nil {
				c.readCond.L.Lock()
				c.readEOF = true
				c.readCond.L.Unlock()
				c.readCond.Broadcast()
				return
			}

			c.readCond.L.Lock()
			c.readBuf.Write(buf[0:n])
			c.readCond.Broadcast()
			c.readCond.L.Unlock()
		}
	}()

	return c
}

func (c *withDeadlineConn) Read(b []byte) (n int, err error) {
	c.readCond.L.Lock()

	if c.readEOF {
		c.readCond.L.Unlock()
		return 0, io.EOF
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if !c.readDeadline.IsZero() {
		// capture with the lock held to prevent goroutine race
		deadline := c.readDeadline

		if time.Now().After(deadline) {
			c.readCond.L.Unlock()
			// return early without calling inner Read
			return 0, os.ErrDeadlineExceeded
		}

		go func() {
			dt := time.Until(deadline)
			if dt > 0 {
				time.Sleep(dt)
			}

			cancel()
		}()
	}

	// Keep track of the reader so a future SetReadDeadline can interrupt it.
	c.readers = append(c.readers, cancel)

	c.readCond.L.Unlock()

	// Start a goroutine for the actual Read operation
	read := make(chan struct{})
	var rN int
	var rerr error
	go func() {
		defer close(read)

		c.readCond.L.Lock()
		defer c.readCond.L.Unlock()

		for ctx.Err() == nil {
			if c.readEOF {
				rerr = io.EOF
				break
			}

			n, _ := c.readBuf.Read(b) // ignore EOF here
			if n > 0 {
				rN = n
				break
			}

			c.readCond.Wait()
		}
	}()

	// Wait for either Read to complete or the timeout
	select {
	case <-read:
		return rN, rerr
	case <-ctx.Done():
		return 0, os.ErrDeadlineExceeded
	}
}

func (c *withDeadlineConn) Write(b []byte) (n int, err error) {
	c.writersL.Lock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if !c.writeDeadline.IsZero() {
		if time.Now().After(c.writeDeadline) {
			c.writersL.Unlock()
			// return early without calling inner Write
			return 0, os.ErrDeadlineExceeded
		}

		go func() {
			dt := time.Until(c.writeDeadline)
			if dt > 0 {
				time.Sleep(dt)
			}

			cancel()
		}()
	}

	// Keep track of the writer so a future SetWriteDeadline can interrupt it.
	c.writers = append(c.writers, cancel)
	c.writersL.Unlock()

	// Start a goroutine for the actual Write operation
	write := make(chan int, 1)
	go func() {
		n, err = c.conn.Write(b)
		write <- 0
	}()

	// Wait for either Write to complete or the timeout
	select {
	case <-write:
		return n, err
	case <-ctx.Done():
		return 0, os.ErrDeadlineExceeded
	}
}

func (c *withDeadlineConn) Close() error {
	return c.conn.Close()
}

func (c *withDeadlineConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

func (c *withDeadlineConn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

func (c *withDeadlineConn) SetDeadline(t time.Time) error {
	return errors.Join(
		c.SetReadDeadline(t),
		c.SetWriteDeadline(t),
	)
}

func (c *withDeadlineConn) SetReadDeadline(t time.Time) error {
	c.readCond.L.Lock()
	c.readDeadline = t
	readers := c.readers
	c.readCond.L.Unlock()

	if len(readers) > 0 && !t.IsZero() {
		go func() {
			dt := time.Until(t)
			if dt > 0 {
				time.Sleep(dt)
			}

			for _, cancel := range readers {
				cancel()
			}
		}()
	}

	return nil
}

func (c *withDeadlineConn) SetWriteDeadline(t time.Time) error {
	c.writersL.Lock()
	c.writeDeadline = t
	writers := c.writers
	c.writersL.Unlock()

	if len(writers) > 0 && !t.IsZero() {
		go func() {
			dt := time.Until(c.writeDeadline)
			if dt > 0 {
				time.Sleep(dt)
			}

			for _, cancel := range writers {
				cancel()
			}
		}()
	}

	return nil
}
