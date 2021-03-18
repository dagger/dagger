package dagger

import (
	"context"
	"sync"

	"dagger.io/go/dagger/compiler"
	"github.com/rs/zerolog/log"
)

type cacheEntry struct {
	p      *Pipeline
	doneCh chan struct{}
}

func (e *cacheEntry) wait() {
	<-e.doneCh
}

func (e *cacheEntry) done() {
	close(e.doneCh)
}

type PipelineManager struct {
	l     sync.Mutex
	cache map[string]*cacheEntry
	s     Solver
}

func NewPipelineManager(s Solver) *PipelineManager {
	return &PipelineManager{
		s:     s,
		cache: make(map[string]*cacheEntry),
	}
}

func (m *PipelineManager) Do(ctx context.Context, v *compiler.Value, out *Fillable) (*Pipeline, error) {
	pv := v.Dereference()
	name := pv.Path().String()

	log.Ctx(ctx).Debug().Str("name", v.Path().String()).Msg("====> " + name)

	// check if the pipeline was already computed and if so, return the cached version
	m.l.Lock()
	if e, ok := m.cache[name]; ok {
		m.l.Unlock()

		// the pipeline could still be in progress, wait for completion
		e.wait()
		return e.p, nil
	}

	// otherwise, create a new pipeline and add it to the cache
	p := NewPipeline(name, m.s, out, m)
	e := &cacheEntry{
		p:      p,
		doneCh: make(chan struct{}),
	}
	m.cache[name] = e
	m.l.Unlock()

	// Compute the pipeline
	defer e.done()
	if err := p.Do(ctx, pv); err != nil {
		return nil, err
	}

	return p, nil
}
