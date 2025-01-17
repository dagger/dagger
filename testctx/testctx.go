// Package testctx provides a generic wrapper around Go's testing types that enables
// context propagation and middleware-based test configuration. It allows for
// structured test suites with OpenTelemetry tracing, timeouts, and other
// context-aware features while maintaining compatibility with standard Go testing
// patterns.
//
// The core type TB[Type] is a generic wrapper around testing.TB implementations
// that preserves all the familiar testing.T and testing.B methods while adding
// context management. Two concrete types are provided for convenience:
//
//	testctx.T = TB[*testing.T]  // For standard tests
//	testctx.B = TB[*testing.B]  // For benchmarks
//
// The package provides two main entry points to integrate with Go's testing machinery:
//
//	testctx.Run()   // For running test suites
//	testctx.Bench() // For running benchmark suites
//
// Example usage for a hypothetical suite named "Module":
//
//	var testCtx = context.Background()
//
//	func TestModule(t *testing.T) {
//	    testctx.Run(testCtx, t, ModuleSuite{}, TestMiddleware()...)
//	}
//
//	func BenchmarkModule(b *testing.B) {
//	    testctx.Bench(testCtx, b, ModuleSuite{}, BenchMiddleware()...)
//	}
//
// The generic Middleware[Type] system allows for writing test configuration code that
// works across both test and benchmark contexts. Some middlewares like WithOTelTracing
// and WithOTelLogging are generic and work with both types, while others like
// WithParallel are specialized for specific test types.
//
// Test methods in suites receive a context.Context and either a *testctx.T or
// *testctx.B, which implement all the familiar methods from testing.T/testing.B:
//
//	type ModuleSuite struct{}
//
//	func (s ModuleSuite) TestSomething(ctx context.Context, t *testctx.T) {
//	    t.Run("subtest", func(ctx context.Context, t *testctx.T) {
//	        // Use context for deadlines, tracing, etc
//	        // Use t.Fatal, t.Error, etc as normal
//	    })
//	}
package testctx

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

type T = TB[*testing.T]
type B = TB[*testing.B]

type MiddlewareT = Middleware[*testing.T]
type MiddlewareB = Middleware[*testing.B]

type Middleware[Type RunnableTB[Type]] func(*TB[Type]) *TB[Type]

type RunnableTB[Type testing.TB] interface {
	testing.TB
	Run(string, func(Type)) bool
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
		// is it possible to also accept func(context.Context, *testing.T) ?
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
		bf, ok := methV.Interface().(func(context.Context, *TB[*testing.B]))
		if !ok {
			testingB.Fatalf("suite method %s does not have the right signature; must be func(*testctx.TB[*testing.B]); have %T",
				methT.Name,
				methV.Interface())
		}

		b.Run(methT.Name, bf)
	}
}

func New[Type RunnableTB[Type]](ctx context.Context, t Type) *TB[Type] {
	// Interrupt the context when the test is done.
	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	return &TB[Type]{
		RunnableTB: t,
		ctx:        ctx,
		mu:         new(sync.Mutex),
	}
}

func WithParallel(t *TB[*testing.T]) *TB[*testing.T] {
	t.RunnableTB.(*testing.T).Parallel()
	return t.
		BeforeEach(func(t *TB[*testing.T]) *TB[*testing.T] {
			t.RunnableTB.(*testing.T).Parallel()
			return t
		})
}

func N(b *TB[*testing.B]) int {
	return b.RunnableTB.(*testing.B).N
}

const TestCtxTypeAttr = "dagger.io/testctx.type"
const TestCtxNameAttr = "dagger.io/testctx.name"
const TestCtxPrewarmAttr = "dagger.io/testctx.prewarm"

func WithOTelTracing[Type RunnableTB[Type]](tracer trace.Tracer) Middleware[Type] {
	wrapSpan := func(t *TB[Type]) *TB[Type] {
		ctx, span := tracer.Start(t.Context(), t.BaseName())
		span.SetAttributes(
			attribute.String(TestCtxTypeAttr, fmt.Sprintf("%T", t.RunnableTB)),
			attribute.String(TestCtxNameAttr, t.Name()),
			attribute.String(TestCtxPrewarmAttr, isPrewarm()),
		)
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
	return func(t *TB[Type]) *TB[Type] {
		return t.
			BeforeAll(wrapSpan).
			BeforeEach(wrapSpan)
	}
}

func isPrewarm() string {
	_, ok := os.LookupEnv("TESTCTX_PREWARM")
	if !ok {
		return "false"
	}
	return "true"
}

func WithOTelLogging[Type RunnableTB[Type]](logger log.Logger) Middleware[Type] {
	return func(t *TB[Type]) *TB[Type] {
		return t.WithLogger(func(t2 *TB[Type], msg string) {
			var rec log.Record
			rec.SetBody(log.StringValue(msg))
			rec.SetTimestamp(time.Now())
			logger.Emit(t2.Context(), rec)
		})
	}
}

func Combine[Type RunnableTB[Type]](middleware ...Middleware[Type]) Middleware[Type] {
	return func(t *TB[Type]) *TB[Type] {
		for _, m := range middleware {
			t = m(t)
		}
		return t
	}
}

type TB[Type RunnableTB[Type]] struct {
	RunnableTB[Type]

	ctx        context.Context
	baseName   string
	logger     func(*TB[Type], string)
	beforeEach []Middleware[Type]
	errors     []string
	mu         *sync.Mutex
}

func (t *TB[Type]) Run(name string, f func(context.Context, *TB[Type])) bool {
	return t.RunnableTB.Run(name, func(testingT Type) {
		sub := t.sub(name, testingT)
		for _, setup := range t.beforeEach {
			sub = setup(sub)
		}
		f(sub.Context(), sub)
	})
}

func (t *TB[Type]) sub(name string, testingT Type) *TB[Type] {
	sub := New(t.ctx, testingT)
	sub.baseName = name
	sub.logger = t.logger
	sub.beforeEach = t.beforeEach
	return sub
}

func (t *TB[Type]) BaseName() string {
	if t.baseName != "" {
		return t.baseName
	}
	return t.Name()
}

func (t *TB[Type]) Context() context.Context {
	return t.ctx
}

func (t TB[Type]) WithContext(ctx context.Context) *TB[Type] {
	t.ctx = ctx
	return &t
}

func (t TB[Type]) WithLogger(logger func(*TB[Type], string)) *TB[Type] {
	t.logger = logger
	return &t
}

func (t *TB[Type]) WithTimeout(timeout time.Duration) *TB[Type] {
	return t.BeforeEach(func(t2 *TB[Type]) *TB[Type] {
		ctx, cancel := context.WithTimeout(t2.Context(), timeout)
		t2.Cleanup(cancel)
		return t2.WithContext(ctx)
	})
}

// BeforeAll calls f immediately with itself and returns the result.
//
// It is not inherited by subtests.
func (t *TB[Type]) BeforeAll(f func(*TB[Type]) *TB[Type]) *TB[Type] {
	return f(t)
}

// BeforeEach configures f to run prior to each subtest.
func (t TB[Type]) BeforeEach(f Middleware[Type]) *TB[Type] {
	cpM := make([]Middleware[Type], len(t.beforeEach))
	copy(cpM, t.beforeEach)
	cpM = append(cpM, f)
	t.beforeEach = cpM
	return &t
}

func (t *TB[Type]) Log(vals ...any) {
	t.log(fmt.Sprintln(vals...))
	t.RunnableTB.Log(vals...)
}

func (t *TB[Type]) Logf(format string, vals ...any) {
	t.logf(format, vals...)
	t.RunnableTB.Logf(format, vals...)
}

func (t *TB[Type]) Error(vals ...any) {
	msg := fmt.Sprint(vals...)
	t.mu.Lock()
	t.errors = append(t.errors, msg)
	t.mu.Unlock()
	t.log(msg)
	t.RunnableTB.Error(vals...)
}

func (t *TB[Type]) Errorf(format string, vals ...any) {
	t.logf(format, vals...)
	t.mu.Lock()
	t.errors = append(t.errors, fmt.Sprintf(format, vals...))
	t.mu.Unlock()
	t.RunnableTB.Errorf(format, vals...)
}

func (t *TB[Type]) Fatal(vals ...any) {
	t.log(fmt.Sprintln(vals...))
	t.mu.Lock()
	t.errors = append(t.errors, fmt.Sprint(vals...))
	t.mu.Unlock()
	t.RunnableTB.Fatal(vals...)
}

func (t *TB[Type]) Fatalf(format string, vals ...any) {
	t.logf(format, vals...)
	t.mu.Lock()
	t.errors = append(t.errors, fmt.Sprintf(format, vals...))
	t.mu.Unlock()
	t.RunnableTB.Fatalf(format, vals...)
}

func (t *TB[Type]) Skip(vals ...any) {
	t.log(fmt.Sprintln(vals...))
	t.RunnableTB.Skip(vals...)
}

func (t *TB[Type]) Skipf(format string, vals ...any) {
	t.logf(format, vals...)
	t.RunnableTB.Skipf(format, vals...)
}

func (t *TB[Type]) Errors() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.Join(t.errors, "\n")
}

func (t *TB[Type]) log(out string) {
	if t.logger != nil {
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		t.logger(t, out)
		return
	}
}

func (t *TB[Type]) logf(format string, vals ...any) {
	if t.logger != nil {
		out := fmt.Sprintf(format, vals...)
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		t.logger(t, out)
		return
	}
}
