package sdklog

import (
	"context"
	"sync"
	"sync/atomic"

	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/embedded"
	"go.opentelemetry.io/otel/log/noop"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	"golang.org/x/sync/errgroup"
)

var _ log.LoggerProvider = &LoggerProvider{}

type LoggerProvider struct {
	embedded.LoggerProvider

	mu         sync.RWMutex
	resource   *resource.Resource
	isShutdown atomic.Bool
	processors []LogProcessor
}

func NewLoggerProvider(resource *resource.Resource) *LoggerProvider {
	return &LoggerProvider{
		resource: resource,
	}
}

func (p *LoggerProvider) Logger(name string, opts ...log.LoggerOption) log.Logger {
	if p.isShutdown.Load() {
		return noop.NewLoggerProvider().Logger(name, opts...)
	}

	c := log.NewLoggerConfig(opts...)
	is := instrumentation.Scope{
		Name:      name,
		Version:   c.InstrumentationVersion(),
		SchemaURL: c.SchemaURL(),
	}

	return &logger{
		provider:             p,
		instrumentationScope: is,
		resource:             p.resource,
	}
}

func (p *LoggerProvider) RegisterLogProcessor(lp LogProcessor) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.isShutdown.Load() {
		return
	}

	p.processors = append(p.processors, lp)
}

func (p *LoggerProvider) Shutdown(ctx context.Context) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	eg := new(errgroup.Group)
	for _, lp := range p.processors {
		lp := lp
		eg.Go(func() error {
			return lp.Shutdown(ctx)
		})
	}
	return eg.Wait()
}

func (p *LoggerProvider) getLogProcessors() []LogProcessor {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.processors
}
