package journal

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
)

type Sink struct {
	Reader

	listener net.Listener
	entriesW Writer
	sources  *sync.WaitGroup
}

func ServeWriters(network, addr string) (*Sink, error) {
	l, err := net.Listen(network, addr)
	if err != nil {
		return nil, err
	}

	r, w := Pipe()

	wg := new(sync.WaitGroup)

	sink := &Sink{
		Reader: r,

		listener: l,
		entriesW: w,
		sources:  wg,
	}

	go sink.accept(l)

	return sink, nil
}

func (sink *Sink) accept(l net.Listener) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}

		sink.sources.Add(1)
		go sink.handle(conn)
	}
}

func (sink *Sink) Endpoint() string {
	addr := sink.Addr()
	return fmt.Sprintf("%s://%s", addr.Network(), addr)
}

func (sink *Sink) Addr() net.Addr {
	return sink.listener.Addr()
}

func (sink *Sink) Close() error {
	if err := sink.listener.Close(); err != nil {
		return err
	}

	sink.sources.Wait()

	return nil
}

func (sink *Sink) handle(conn net.Conn) {
	defer sink.sources.Done()

	dec := json.NewDecoder(conn)

	for {
		var status Entry
		if err := dec.Decode(&status); err != nil {
			return
		}

		sink.entriesW.WriteEntry(&status)
	}
}

type netWriter struct {
	c   net.Conn
	enc *json.Encoder
}

func Dial(network, addr string) (Writer, error) {
	c, err := net.Dial(network, addr)
	if err != nil {
		return nil, err
	}

	return &netWriter{
		c:   c,
		enc: json.NewEncoder(c),
	}, nil
}

func (w *netWriter) WriteEntry(status *Entry) error {
	return w.enc.Encode(status)
}

func (w *netWriter) Close() error {
	return w.c.Close()
}
