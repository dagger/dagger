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

type Middleware = func(ITB) ITB

func Run(ctx context.Context, testingT *testing.T, suite any, middleware ...Middleware) {
	t := New(ctx, testingT)
	for _, m := range middleware {
		result := m(t)
		if mt, ok := result.(*TB); ok {
			t.TB = mt
		} else {
			t.Fatalf("middleware returned wrong type, expected *TB, got %T", result)
		}
	}

	suiteT := reflect.TypeOf(suite)
	suiteV := reflect.ValueOf(suite)

	for i := range suiteV.NumMethod() {
		methT := suiteT.Method(i)
		if !strings.HasPrefix(methT.Name, "Test") {
			continue
		}

		methV := suiteV.Method(i)
		tf, ok := methV.Interface().(func(context.Context, *T))
		if !ok {
			testingT.Fatalf("suite method %s does not have the right signature; must be func(testctx.T); have %T",
				methT.Name,
				methV.Interface())
		}

		t.Run(methT.Name, tf)
	}
}

type T struct {
	*TB
	T *testing.T
}

func New(ctx context.Context, t *testing.T) *T {
	// Interrupt the context when the test is done.
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	return &T{
		TB: &TB{
			TB:  t,
			ctx: ctx,
			mu:  new(sync.Mutex),
		},
		T: t,
	}
}

func (t *T) Run(name string, f func(context.Context, *T)) bool {
	return t.T.Run(name, func(testingT *testing.T) {
		sub := t.sub(name, testingT)
		for _, setup := range t.beforeEach {
			sub.TB = setup(sub.TB).(*TB)
		}
		f(sub.Context(), sub)
	})
}

func (t *T) sub(name string, testingT *testing.T) *T {
	sub := New(t.ctx, testingT)
	sub.baseName = name
	sub.logger = t.logger
	sub.beforeEach = t.beforeEach
	return sub
}

func Bench(ctx context.Context, testingB *testing.B, suite any, middleware ...Middleware) {
	b := NewBench(ctx, testingB)
	for _, m := range middleware {
		result := m(b)
		if mb, ok := result.(*TB); ok {
			b.TB = mb
		} else {
			b.Fatalf("middleware returned wrong type, expected *B, got %T", result)
		}
	}

	suiteT := reflect.TypeOf(suite)
	suiteV := reflect.ValueOf(suite)

	for i := range suiteV.NumMethod() {
		methT := suiteT.Method(i)
		if !strings.HasPrefix(methT.Name, "Benchmark") {
			continue
		}

		methV := suiteV.Method(i)
		var bf func(context.Context, *B)
		switch fn := methV.Interface().(type) {
		case func(context.Context, *B):
			bf = fn
		case func(context.Context, ITB):
			bf = func(ctx context.Context, b *B) {
				fn(ctx, b)
			}
		default:
			testingB.Fatalf("suite method %s does not have the right signature; must be func(context.Context, *testctx.B) or func(context.Context, ITB); have %T",
				methT.Name,
				methV.Interface())
		}

		b.Run(methT.Name, bf)
	}
}

type B struct {
	*TB
	B *testing.B
}

func (b *B) Run(name string, f func(context.Context, *B)) bool {
	return b.B.Run(name, func(testingB *testing.B) {
		sub := b.sub(name, testingB)
		for _, setup := range b.beforeEach {
			sub.TB = setup(sub.TB).(*TB)
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

func NewBench(ctx context.Context, b *testing.B) *B {
	// Interrupt the context when the benchmark is done.
	ctx, cancel := context.WithCancel(ctx)
	b.Cleanup(cancel)
	return &B{
		TB: &TB{
			TB:  b,
			ctx: ctx,
			mu:  new(sync.Mutex),
		},
		B: b,
	}
}

func WithParallel(t ITB) ITB {
	if tt, ok := t.TTB().(*testing.T); ok {
		tt.Parallel()
	} else {
		t.Fatal("TB is not *testing.T, cannot call Parallel()")
	}
	return t.
		BeforeEach(func(t ITB) ITB {
			if tt, ok := t.TTB().(*testing.T); ok {
				tt.Parallel()
			} else {
				t.Fatal("TB is not *testing.T, cannot call Parallel()")
			}
			return t
		})
}

func WithOTelTracing(tracer trace.Tracer) Middleware {
	wrapSpan := func(t ITB) ITB {
		ctx, span := tracer.Start(t.Context(), t.BaseName())
		t.Cleanup(func() {
			if t.Failed() {
				span.SetStatus(codes.Error, "test failed")
			} else {
				span.SetStatus(codes.Ok, "")
			}
			span.End()
		})
		return t.WithContext(ctx)
	}
	return func(t ITB) ITB {
		return t.
			BeforeAll(wrapSpan).
			BeforeEach(wrapSpan)
	}
}

func WithOTelLogging(logger log.Logger) Middleware {
	return func(t ITB) ITB {
		return t.WithLogger(func(t2 ITB, msg string) {
			var rec log.Record
			rec.SetBody(log.StringValue(msg))
			rec.SetTimestamp(time.Now())
			logger.Emit(t2.Context(), rec)
		})
	}
}

func Combine(middleware ...Middleware) Middleware {
	return func(t ITB) ITB {
		for _, m := range middleware {
			t = m(t)
		}
		return t
	}
}

// TODO private
type TB struct {
	testing.TB

	ctx        context.Context
	baseName   string
	logger     func(ITB, string)
	beforeEach []Middleware
	errors     []string
	mu         *sync.Mutex
}

// TODO rename
type ITB interface {
	testing.TB
	TTB() testing.TB

	BaseName() string
	BeforeEach(Middleware) ITB
	BeforeAll(func(ITB) ITB) ITB
	Context() context.Context
	WithContext(context.Context) ITB
	WithLogger(func(ITB, string)) ITB
	WithTimeout(timeout time.Duration) ITB
}

func (t *TB) TTB() testing.TB {
	return t.TB
}

func (t *TB) BaseName() string {
	if t.baseName != "" {
		return t.baseName
	}
	return t.Name()
}

func (t *TB) Context() context.Context {
	return t.ctx
}

func (t TB) WithContext(ctx context.Context) ITB {
	t.ctx = ctx
	return &t
}

func (t TB) WithLogger(logger func(ITB, string)) ITB {
	t.logger = logger
	return &t
}

func (t *TB) WithTimeout(timeout time.Duration) ITB {
	return t.BeforeEach(func(t2 ITB) ITB {
		ctx, cancel := context.WithTimeout(t2.Context(), timeout)
		t2.Cleanup(cancel)
		return t2.WithContext(ctx)
	})
}

// BeforeAll calls f immediately with itself and returns the result.
//
// It is not inherited by subtests.
func (t *TB) BeforeAll(f func(ITB) ITB) ITB {
	return f(t)
}

// BeforeEach configures f to run prior to each subtest.
func (t TB) BeforeEach(f Middleware) ITB {
	cpM := make([]Middleware, len(t.beforeEach))
	copy(cpM, t.beforeEach)
	cpM = append(cpM, f)
	t.beforeEach = cpM
	return &t
}

func (t *TB) Log(vals ...any) {
	t.log(fmt.Sprintln(vals...))
	t.TB.Log(vals...)
}

func (t *TB) Logf(format string, vals ...any) {
	t.logf(format, vals...)
	t.TB.Logf(format, vals...)
}

func (t *TB) Error(vals ...any) {
	msg := fmt.Sprint(vals...)
	t.mu.Lock()
	t.errors = append(t.errors, msg)
	t.mu.Unlock()
	t.log(msg)
	t.TB.Error(vals...)
}

func (t *TB) Errorf(format string, vals ...any) {
	t.logf(format, vals...)
	t.mu.Lock()
	t.errors = append(t.errors, fmt.Sprintf(format, vals...))
	t.mu.Unlock()
	t.TB.Errorf(format, vals...)
}

func (t *TB) Fatal(vals ...any) {
	t.log(fmt.Sprintln(vals...))
	t.mu.Lock()
	t.errors = append(t.errors, fmt.Sprint(vals...))
	t.mu.Unlock()
	t.TB.Fatal(vals...)
}

func (t *TB) Fatalf(format string, vals ...any) {
	t.logf(format, vals...)
	t.mu.Lock()
	t.errors = append(t.errors, fmt.Sprintf(format, vals...))
	t.mu.Unlock()
	t.TB.Fatalf(format, vals...)
}

func (t *TB) Skip(vals ...any) {
	t.log(fmt.Sprintln(vals...))
	t.TB.Skip(vals...)
}

func (t *TB) Skipf(format string, vals ...any) {
	t.logf(format, vals...)
	t.TB.Skipf(format, vals...)
}

func (t *TB) Errors() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.errors, "\n")
}

func (t *TB) log(out string) {
	if t.logger != nil {
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		t.logger(t, out)
		return
	}
}

func (t *TB) logf(format string, vals ...any) {
	if t.logger != nil {
		out := fmt.Sprintf(format, vals...)
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		t.logger(t, out)
		return
	}
}
