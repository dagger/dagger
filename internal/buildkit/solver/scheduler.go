package solver

import (
	"context"
	"sync"

	"github.com/dagger/dagger/internal/buildkit/errdefs"
	"github.com/dagger/dagger/internal/buildkit/solver/internal/pipe"
	"github.com/dagger/dagger/internal/buildkit/util/cond"
	"github.com/pkg/errors"
)

func newScheduler(ef edgeFactory) *scheduler {
	s := &scheduler{
		waitq:    map[*edge]struct{}{},
		incoming: map[*edge][]*edgePipe{},
		outgoing: map[*edge][]*edgePipe{},

		stopped: make(chan struct{}),
		closed:  make(chan struct{}),

		ef: ef,
	}
	s.cond = cond.NewStatefulCond(&s.mu)

	go s.loop()

	return s
}

type dispatcher struct {
	next *dispatcher
	e    *edge
}

type scheduler struct {
	cond *cond.StatefulCond
	mu   sync.Mutex
	muQ  sync.Mutex

	ef edgeFactory

	waitq       map[*edge]struct{}
	next        *dispatcher
	last        *dispatcher
	stopped     chan struct{}
	stoppedOnce sync.Once
	closed      chan struct{}

	incoming map[*edge][]*edgePipe
	outgoing map[*edge][]*edgePipe
}

func (s *scheduler) Stop() {
	s.stoppedOnce.Do(func() {
		close(s.stopped)
	})
	<-s.closed
}

func (s *scheduler) loop() {
	defer func() {
		close(s.closed)
	}()

	go func() {
		<-s.stopped
		s.mu.Lock()
		s.cond.Signal()
		s.mu.Unlock()
	}()

	s.mu.Lock()
	for {
		select {
		case <-s.stopped:
			s.mu.Unlock()
			return
		default:
		}
		s.muQ.Lock()
		l := s.next
		if l != nil {
			if l == s.last {
				s.last = nil
			}
			s.next = l.next
			delete(s.waitq, l.e)
		}
		s.muQ.Unlock()
		if l == nil {
			s.cond.Wait()
			continue
		}
		s.dispatch(l.e)
	}
}

// dispatch schedules an edge to be processed
func (s *scheduler) dispatch(e *edge) {
	inc := make([]pipeSender, len(s.incoming[e]))
	for i, p := range s.incoming[e] {
		inc[i] = p.Sender
	}
	out := make([]pipeReceiver, len(s.outgoing[e]))
	for i, p := range s.outgoing[e] {
		out[i] = p.Receiver
	}

	e.hasActiveOutgoing = false
	updates := []pipeReceiver{}
	for _, p := range out {
		if ok := p.Receive(); ok {
			updates = append(updates, p)
		}
		if !p.Status().Completed {
			e.hasActiveOutgoing = true
		}
	}

	pf := &pipeFactory{s: s, e: e}

	// unpark the edge
	debugSchedulerPreUnpark(e, inc, updates, out)
	e.unpark(inc, updates, out, pf)
	debugSchedulerPostUnpark(e, inc)

	// set up new requests that didn't complete/were added by this run
	openIncoming := make([]*edgePipe, 0, len(inc))
	for _, r := range s.incoming[e] {
		if !r.Sender.Status().Completed {
			openIncoming = append(openIncoming, r)
		}
	}
	if len(openIncoming) > 0 {
		s.incoming[e] = openIncoming
	} else {
		delete(s.incoming, e)
	}

	openOutgoing := make([]*edgePipe, 0, len(out))
	for _, r := range s.outgoing[e] {
		if !r.Receiver.Status().Completed {
			openOutgoing = append(openOutgoing, r)
		}
	}
	if len(openOutgoing) > 0 {
		s.outgoing[e] = openOutgoing
	} else {
		delete(s.outgoing, e)
	}

	// if keys changed there might be possiblity for merge with other edge
	if e.keysDidChange {
		if k := e.currentIndexKey(); k != nil {
			// skip this if not at least 1 key per dep
			// NOTE: we disabled edge merging here, but leaving in this LoadOrStore since
			// it has side-effects and who knows if it's load-bearing or not
			_ = e.index.LoadOrStore(k, e)
		}
		e.keysDidChange = false
	}

	// validation to avoid deadlocks/resource leaks:
	// TODO: if these start showing up in error reports they can be changed
	// to error the edge instead. They can only appear from algorithm bugs in
	// unpark(), not for any external input.
	if len(openIncoming) > 0 && len(openOutgoing) == 0 {
		e.markFailed(pf, errors.New("buildkit scheduler error: return leaving incoming open. Please report this with BUILDKIT_SCHEDULER_DEBUG=1"))
	}
	if len(openIncoming) == 0 && len(openOutgoing) > 0 {
		e.markFailed(pf, errors.New("buildkit scheduler error: return leaving outgoing open. Please report this with BUILDKIT_SCHEDULER_DEBUG=1"))
	}
}

// signal notifies that an edge needs to be processed again
func (s *scheduler) signal(e *edge) {
	s.muQ.Lock()
	if _, ok := s.waitq[e]; !ok {
		d := &dispatcher{e: e}
		if s.last == nil {
			s.next = d
		} else {
			s.last.next = d
		}
		s.last = d
		s.waitq[e] = struct{}{}
		s.cond.Signal()
	}
	s.muQ.Unlock()
}

// build evaluates edge into a result
func (s *scheduler) build(ctx context.Context, edge Edge) (CachedResult, error) {
	s.mu.Lock()
	e := s.ef.getEdge(edge)
	if e == nil {
		s.mu.Unlock()
		return nil, errors.Errorf("invalid request %v for build", edge)
	}

	wait := make(chan struct{})

	p := s.newPipe(e, nil, pipeRequest{Payload: &edgeRequest{desiredState: edgeStatusComplete}})
	p.OnSendCompletion = func() {
		p.Receiver.Receive()
		if p.Receiver.Status().Completed {
			close(wait)
		}
	}
	s.mu.Unlock()

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(errors.WithStack(context.Canceled))

	go func() {
		<-ctx.Done()
		p.Receiver.Cancel()
	}()

	<-wait

	if err := p.Receiver.Status().Err; err != nil {
		return nil, err
	}
	return p.Receiver.Status().Value.(*edgeState).result.CloneCachedResult(), nil
}

// newPipe creates a new request pipe between two edges
func (s *scheduler) newPipe(target, from *edge, req pipeRequest) *pipe.Pipe[*edgeRequest, any] {
	p := &edgePipe{
		Pipe:   newPipe(req),
		Target: target,
		From:   from,
	}

	s.signal(target)
	if from != nil {
		p.OnSendCompletion = func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			s.signal(p.From)
		}
		s.outgoing[from] = append(s.outgoing[from], p)
	}
	s.incoming[target] = append(s.incoming[target], p)
	p.OnReceiveCompletion = func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		s.signal(p.Target)
	}
	return p.Pipe
}

// newRequestWithFunc creates a new request pipe that invokes a async function
func (s *scheduler) newRequestWithFunc(e *edge, f func(context.Context) (any, error)) pipeReceiver {
	pp, start := pipe.NewWithFunction[*edgeRequest](f)
	p := &edgePipe{
		Pipe: pp,
		From: e,
	}
	p.OnSendCompletion = func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		s.signal(p.From)
	}
	s.outgoing[e] = append(s.outgoing[e], p)
	go start()
	return p.Receiver
}

// edgeFactory allows access to the edges from a shared graph
type edgeFactory interface {
	getEdge(Edge) *edge
}

type pipeFactory struct {
	e *edge
	s *scheduler
}

func (pf *pipeFactory) NewInputRequest(ee Edge, req *edgeRequest) pipeReceiver {
	target := pf.s.ef.getEdge(ee)
	if target == nil {
		debugSchedulerInconsistentGraphState(ee)
		return pf.NewFuncRequest(func(_ context.Context) (interface{}, error) {
			return nil, errdefs.Internal(errors.Errorf("failed to get edge: inconsistent graph state in edge %s %s %d", ee.Vertex.Name(), ee.Vertex.Digest(), ee.Index))
		})
	}
	p := pf.s.newPipe(target, pf.e, pipeRequest{Payload: req})
	debugSchedulerNewPipe(pf.e, p, req)
	return p.Receiver
}

func (pf *pipeFactory) NewFuncRequest(f func(context.Context) (interface{}, error)) pipeReceiver {
	p := pf.s.newRequestWithFunc(pf.e, f)
	debugSchedulerNewFunc(pf.e, p)
	return p
}
