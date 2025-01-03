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

type T = TB[*testing.T]
type B = TB[*testing.B]

type MiddlewareT = Middleware[*testing.T]
type MiddlewareB = Middleware[*testing.B]

type Middleware[T ITB[T]] func(*TB[T]) *TB[T]

type ITB[T testing.TB] interface {
	testing.TB
	Run(string, func(T)) bool
}

func Run(ctx context.Context, testingT *testing.T, suite any, middleware ...Middleware[*testing.T]) {
	t := New(ctx, testingT)
	for _, m := range middleware {
		t = m(t)
	}

	suiteT := reflect.TypeOf(suite)
	suiteV := reflect.ValueOf(suite)

	for i := range suiteV.NumMethod() {
		methT := suiteT.Method(i)
		if !strings.HasPrefix(methT.Name, "Test") {
			continue
		}

		methV := suiteV.Method(i)
		// TODO: also accept func(context.Context, *testing.T) ?
		tf, ok := methV.Interface().(func(context.Context, *TB[*testing.T]))
		if !ok {
			testingT.Fatalf("suite method %s does not have the right signature; must be func(testctx.T); have %T",
				methT.Name,
				methV.Interface())
		}

		t.Run(methT.Name, tf)
	}
}

func Bench(ctx context.Context, testingB *testing.B, suite any, middleware ...Middleware[*testing.B]) {
	b := New(ctx, testingB)
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
		// TODO: also accept func(context.Context, *testing.B) ?
		bf, ok := methV.Interface().(func(context.Context, *TB[*testing.B]))
		if !ok {
			testingB.Fatalf("suite method %s does not have the right signature; must be func(*testctx.TB[*testing.B]); have %T",
				methT.Name,
				methV.Interface())
		}

		b.Run(methT.Name, bf)
	}
}

func New[T ITB[T]](ctx context.Context, t T) *TB[T] {
	// Interrupt the context when the test is done.
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	return &TB[T]{
		ITB: t,
		ctx: ctx,
		mu:  new(sync.Mutex),
	}
}

func WithParallel(t *TB[*testing.T]) *TB[*testing.T] {
	t.ITB.(*testing.T).Parallel()
	return t.
		BeforeEach(func(t *TB[*testing.T]) *TB[*testing.T] {
			t.ITB.(*testing.T).Parallel()
			return t
		})
}

func WithOTelTracing[T ITB[T]](tracer trace.Tracer) Middleware[T] {
	wrapSpan := func(t *TB[T]) *TB[T] {
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
	return func(t *TB[T]) *TB[T] {
		return t.
			BeforeAll(wrapSpan).
			BeforeEach(wrapSpan)
	}
}

func WithOTelLogging[T ITB[T]](logger log.Logger) Middleware[T] {
	return func(t *TB[T]) *TB[T] {
		return t.WithLogger(func(t2 *TB[T], msg string) {
			var rec log.Record
			rec.SetBody(log.StringValue(msg))
			rec.SetTimestamp(time.Now())
			logger.Emit(t2.Context(), rec)
		})
	}
}

func Combine[T ITB[T]](middleware ...Middleware[T]) Middleware[T] {
	return func(t *TB[T]) *TB[T] {
		for _, m := range middleware {
			t = m(t)
		}
		return t
	}
}

type TB[T ITB[T]] struct {
	ITB[T]

	ctx        context.Context
	baseName   string
	logger     func(*TB[T], string)
	beforeEach []Middleware[T]
	errors     []string
	mu         *sync.Mutex
}

func (t *TB[T]) Run(name string, f func(context.Context, *TB[T])) bool {
	return t.ITB.Run(name, func(testingT T) {
		sub := t.sub(name, testingT)
		for _, setup := range t.beforeEach {
			sub = setup(sub)
		}
		f(sub.Context(), sub)
	})
}

func (t *TB[T]) sub(name string, testingT T) *TB[T] {
	sub := New(t.ctx, testingT)
	sub.baseName = name
	sub.logger = t.logger
	sub.beforeEach = t.beforeEach
	return sub
}

func (t *TB[T]) BaseName() string {
	if t.baseName != "" {
		return t.baseName
	}
	return t.Name()
}

func (t *TB[T]) Context() context.Context {
	return t.ctx
}

func (t TB[T]) WithContext(ctx context.Context) *TB[T] {
	t.ctx = ctx
	return &t
}

func (t TB[T]) WithLogger(logger func(*TB[T], string)) *TB[T] {
	t.logger = logger
	return &t
}

func (t *TB[T]) WithTimeout(timeout time.Duration) *TB[T] {
	return t.BeforeEach(func(t2 *TB[T]) *TB[T] {
		ctx, cancel := context.WithTimeout(t2.Context(), timeout)
		t2.Cleanup(cancel)
		return t2.WithContext(ctx)
	})
}

// BeforeAll calls f immediately with itself and returns the result.
//
// It is not inherited by subtests.
func (t *TB[T]) BeforeAll(f func(*TB[T]) *TB[T]) *TB[T] {
	return f(t)
}

// BeforeEach configures f to run prior to each subtest.
func (t TB[T]) BeforeEach(f Middleware[T]) *TB[T] {
	cpM := make([]Middleware[T], len(t.beforeEach))
	copy(cpM, t.beforeEach)
	cpM = append(cpM, f)
	t.beforeEach = cpM
	return &t
}

func (t *TB[T]) Log(vals ...any) {
	t.log(fmt.Sprintln(vals...))
	t.ITB.Log(vals...)
}

func (t *TB[T]) Logf(format string, vals ...any) {
	t.logf(format, vals...)
	t.ITB.Logf(format, vals...)
}

func (t *TB[T]) Error(vals ...any) {
	msg := fmt.Sprint(vals...)
	t.mu.Lock()
	t.errors = append(t.errors, msg)
	t.mu.Unlock()
	t.log(msg)
	t.ITB.Error(vals...)
}

func (t *TB[T]) Errorf(format string, vals ...any) {
	t.logf(format, vals...)
	t.mu.Lock()
	t.errors = append(t.errors, fmt.Sprintf(format, vals...))
	t.mu.Unlock()
	t.ITB.Errorf(format, vals...)
}

func (t *TB[T]) Fatal(vals ...any) {
	t.log(fmt.Sprintln(vals...))
	t.mu.Lock()
	t.errors = append(t.errors, fmt.Sprint(vals...))
	t.mu.Unlock()
	t.ITB.Fatal(vals...)
}

func (t *TB[T]) Fatalf(format string, vals ...any) {
	t.logf(format, vals...)
	t.mu.Lock()
	t.errors = append(t.errors, fmt.Sprintf(format, vals...))
	t.mu.Unlock()
	t.ITB.Fatalf(format, vals...)
}

func (t *TB[T]) Skip(vals ...any) {
	t.log(fmt.Sprintln(vals...))
	t.ITB.Skip(vals...)
}

func (t *TB[T]) Skipf(format string, vals ...any) {
	t.logf(format, vals...)
	t.ITB.Skipf(format, vals...)
}

func (t *TB[T]) Errors() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.errors, "\n")
}

func (t *TB[T]) log(out string) {
	if t.logger != nil {
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		t.logger(t, out)
		return
	}
}

func (t *TB[T]) logf(format string, vals ...any) {
	if t.logger != nil {
		out := fmt.Sprintf(format, vals...)
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		t.logger(t, out)
		return
	}
}
