package engine

import (
	"fmt"
	"net"
	"net/rpc"
	"sync"
	"time"

	bkclient "github.com/moby/buildkit/client"
)

type JournalWriter interface {
	WriteStatus(*JournalEntry) error
	Close() error
}

type JournalReader interface {
	ReadStatus() (*JournalEntry, bool)
}

type JournalEntry struct {
	Event *bkclient.SolveStatus
	TS    time.Time
}

type RPCWriter struct {
	c *rpc.Client
}

func ServeRPC(l net.Listener) (JournalReader, JournalWriter, error) {
	r, w := Pipe()

	wg := new(sync.WaitGroup)

	recv := &RPCReceiver{
		w:               w,
		attachedClients: wg,
	}

	s := rpc.NewServer()
	err := s.Register(recv)
	if err != nil {
		return nil, nil, err
	}

	go s.Accept(l)

	return r, WaitWriter{
		JournalWriter: w,

		attachedClients: wg,
	}, nil
}

func DialRPC(net, addr string) (JournalWriter, error) {
	c, err := rpc.Dial(net, addr)
	if err != nil {
		return nil, err
	}

	var res NoResponse
	err = c.Call("RPCReceiver.Attach", &NoArgs{}, &res)
	if err != nil {
		return nil, fmt.Errorf("attach: %w", err)
	}

	return &RPCWriter{
		c: c,
	}, nil
}

func (w *RPCWriter) WriteStatus(status *JournalEntry) error {
	var res NoResponse
	return w.c.Call("RPCReceiver.Write", status, &res)
}

func (w *RPCWriter) Close() error {
	var res NoResponse
	return w.c.Call("RPCReceiver.Detach", &NoArgs{}, &res)
}

type RPCReceiver struct {
	w               JournalWriter
	attachedClients *sync.WaitGroup
}

type NoArgs struct{}
type NoResponse struct{}

func (recv *RPCReceiver) Attach(*NoArgs, *NoResponse) error {
	recv.attachedClients.Add(1)
	return nil
}

func (recv *RPCReceiver) Write(status *JournalEntry, res *NoResponse) error {
	recv.w.WriteStatus(status)
	return nil
}

func (recv *RPCReceiver) Detach(*NoArgs, *NoResponse) error {
	recv.attachedClients.Done()
	return nil
}

type WaitWriter struct {
	JournalWriter
	attachedClients *sync.WaitGroup
}

func (ww WaitWriter) Close() error {
	ww.attachedClients.Wait()
	return ww.JournalWriter.Close()
}

// PipeBuffer is the number of writes to allow before a read must occur.
//
// This value is arbitrary. I don't want to leak this implementation detail to
// the API because it feels like a bit of a smell that this is even necessary,
// and perhaps the rest of the API can be refactored instead.
//
// The main idea is to be nonzero to prevent any weird deadlocks that occur
// around initialization/exit time due to an error in an inopportune moment.
// I'm tired of chasing down deadlocks and this seems like a low risk band-aid.
// Refactors welcome.
const PipeBuffer = 5

func Pipe() (JournalReader, JournalWriter) {
	ch := make(chan *JournalEntry, PipeBuffer)
	return JournalReaderChan(ch), &chanWriter{ch: ch}
}

type chanWriter struct {
	ch chan<- *JournalEntry

	sync.Mutex
}

func (doc *chanWriter) WriteStatus(v *JournalEntry) error {
	doc.Lock()
	defer doc.Unlock()

	if doc.ch == nil {
		// discard
		return nil
	}

	doc.ch <- v
	return nil
}

func (doc *chanWriter) Close() error {
	doc.Lock()
	if doc.ch != nil {
		close(doc.ch)
		doc.ch = nil
	}
	doc.Unlock()
	return nil
}

type JournalReaderChan <-chan *JournalEntry // uwu

func (ch JournalReaderChan) ReadStatus() (*JournalEntry, bool) {
	val, ok := <-ch
	return val, ok
}
