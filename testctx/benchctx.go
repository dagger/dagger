package testctx

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

type MiddlewareB = func(*B) *B

func Bench(ctx context.Context, testingB *testing.B, suite any, middleware ...MiddlewareB) {
	b := NewBench(ctx, testingB)
	for _, m := range middleware {
		b = m(b)
	}

	suiteT := reflect.TypeOf(suite)
	suiteV := reflect.ValueOf(suite)

	for i := range suiteV.NumMethod() {
		methT := suiteT.Method(i)
		if !strings.HasPrefix(methT.Name, "Benchmark") {
			continue
		}

		methV := suiteV.Method(i)
		bf, ok := methV.Interface().(func(context.Context, *B))
		if !ok {
			testingB.Fatalf("suite method %s does not have the right signature; must be func(context.Context, *testctx.B); have %T",
				methT.Name,
				methV.Interface())
		}

		b.Run(methT.Name, bf)
	}
}

func WithOTelTracingB(tracer trace.Tracer) MiddlewareB {
	wrapSpan := func(b *B) *B {
		ctx, span := tracer.Start(b.Context(), b.BaseName())
		b.Cleanup(func() {
			if b.Failed() {
				span.SetStatus(codes.Error, "benchmark failed")
			} else {
				span.SetStatus(codes.Ok, "")
			}
		})
		return b.WithContext(ctx)
	}
	return func(b *B) *B {
		return b.
			BeforeAll(wrapSpan).
			BeforeEach(wrapSpan)
	}
}

func WithOTelLoggingB(logger log.Logger) MiddlewareB {
	return func(b *B) *B {
		return b.WithLogger(func(b2 *B, msg string) {
			var rec log.Record
			rec.SetBody(log.StringValue(msg))
			rec.SetTimestamp(time.Now())
			logger.Emit(b2.Context(), rec)
		})
	}
}

func CombineB(middleware ...MiddlewareB) MiddlewareB {
	return func(b *B) *B {
		for _, m := range middleware {
			b = m(b)
		}
		return b
	}
}

func NewBench(ctx context.Context, b *testing.B) *B {
	// Interrupt the context when the benchmark is done.
	ctx, cancel := context.WithCancel(ctx)
	b.Cleanup(cancel)
	return &B{
		B:   b,
		ctx: ctx,
		mu:  new(sync.Mutex),
	}
}

type B struct {
	*testing.B

	ctx        context.Context
	baseName   string
	logger     func(*B, string)
	beforeEach []func(*B) *B
	errors     []string
	mu         *sync.Mutex
}

func (b *B) BaseName() string {
	if b.baseName != "" {
		return b.baseName
	}
	return b.Name()
}

func (b *B) Context() context.Context {
	return b.ctx
}

func (b B) WithContext(ctx context.Context) *B {
	b.ctx = ctx
	return &b
}

func (b B) WithLogger(logger func(*B, string)) *B {
	b.logger = logger
	return &b
}

func (b *B) WithTimeout(timeout time.Duration) *B {
	return b.BeforeEach(func(b2 *B) *B {
		ctx, cancel := context.WithTimeout(b2.Context(), timeout)
		b2.Cleanup(cancel)
		return b2.WithContext(ctx)
	})
}

// BeforeAll calls f immediately with itself and returns the result.
//
// It is not inherited by subtests.
func (b *B) BeforeAll(f func(*B) *B) *B {
	return f(b)
}

// BeforeEach configures f to run prior to each benchmark.
func (b B) BeforeEach(f MiddlewareB) *B {
	cpM := make([]MiddlewareB, len(b.beforeEach))
	copy(cpM, b.beforeEach)
	cpM = append(cpM, f)
	b.beforeEach = cpM
	return &b
}

func (b *B) Run(name string, f func(context.Context, *B)) bool {
	return b.B.Run(name, func(testingB *testing.B) {
		sub := b.sub(name, testingB)
		for _, setup := range b.beforeEach {
			sub = setup(sub)
		}
		f(sub.Context(), sub)
	})
}

func (b *B) sub(name string, testingB *testing.B) *B {
	sub := NewBench(b.ctx, testingB)
	sub.baseName = name
	sub.logger = b.logger
	sub.beforeEach = b.beforeEach
	return sub
}

func (b *B) Log(vals ...any) {
	b.log(fmt.Sprintln(vals...))
	b.B.Log(vals...)
}

func (b *B) Logf(format string, vals ...any) {
	b.logf(format, vals...)
	b.B.Logf(format, vals...)
}

func (b *B) Error(vals ...any) {
	msg := fmt.Sprint(vals...)
	b.mu.Lock()
	b.errors = append(b.errors, msg)
	b.mu.Unlock()
	b.log(msg)
	b.B.Error(vals...)
}

func (b *B) Errorf(format string, vals ...any) {
	b.logf(format, vals...)
	b.mu.Lock()
	b.errors = append(b.errors, fmt.Sprintf(format, vals...))
	b.mu.Unlock()
	b.B.Errorf(format, vals...)
}

func (b *B) Fatal(vals ...any) {
	b.log(fmt.Sprintln(vals...))
	b.mu.Lock()
	b.errors = append(b.errors, fmt.Sprint(vals...))
	b.mu.Unlock()
	b.B.Fatal(vals...)
}

func (b *B) Fatalf(format string, vals ...any) {
	b.logf(format, vals...)
	b.mu.Lock()
	b.errors = append(b.errors, fmt.Sprintf(format, vals...))
	b.mu.Unlock()
	b.B.Fatalf(format, vals...)
}

func (b *B) Skip(vals ...any) {
	b.log(fmt.Sprintln(vals...))
	b.B.Skip(vals...)
}

func (b *B) Skipf(format string, vals ...any) {
	b.logf(format, vals...)
	b.B.Skipf(format, vals...)
}

func (b *B) Errors() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Join(b.errors, "\n")
}

func (b *B) log(out string) {
	if b.logger != nil {
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		b.logger(b, out)
		return
	}
}

func (b *B) logf(format string, vals ...any) {
	if b.logger != nil {
		out := fmt.Sprintf(format, vals...)
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		b.logger(b, out)
		return
	}
}
