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

type Middleware = func(*TB) *TB

func WithParallel(t *TB) *TB {
	if tt, ok := t.TB.(*testing.T); ok {
		tt.Parallel()
	} else {
		t.Fatal("TB is not *testing.T, cannot call Parallel()")
	}
	return t.
		BeforeEach(func(t *TB) *TB {
			if tt, ok := t.TB.(*testing.T); ok {
				tt.Parallel()
			} else {
				t.Fatal("TB is not *testing.T, cannot call Parallel()")
			}
			return t
		})
}

func Run(ctx context.Context, testingT *testing.T, suite any, middleware ...Middleware) {
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
		tf, ok := methV.Interface().(func(context.Context, *T))
		if !ok {
			testingT.Fatalf("suite method %s does not have the right signature; must be func(testctx.T); have %T",
				methT.Name,
				methV.Interface())
		}

		t.Run(methT.Name, tf)
	}
}

func WithOTelTracing(tracer trace.Tracer) Middleware {
	wrapSpan := func(t *TB) *TB {
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
	return func(t *TB) *TB {
		return t.
			BeforeAll(wrapSpan).
			BeforeEach(wrapSpan)
	}
}

func WithOTelLogging(logger log.Logger) Middleware {
	return func(t *TB) *TB {
		return t.WithLogger(func(t2 *TB, msg string) {
			var rec log.Record
			rec.SetBody(log.StringValue(msg))
			rec.SetTimestamp(time.Now())
			logger.Emit(t2.Context(), rec)
		})
	}
}

func Combine(middleware ...Middleware) Middleware {
	return func(t *TB) *TB {
		for _, m := range middleware {
			t = m(t)
		}
		return t
	}
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

type TB struct {
	testing.TB

	ctx        context.Context
	baseName   string
	logger     func(*TB, string)
	beforeEach []func(*TB) *TB
	errors     []string
	mu         *sync.Mutex
}

type T struct {
	*TB
	*testing.T
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

func (t TB) WithContext(ctx context.Context) *TB {
	t.ctx = ctx
	return &t
}

func (t TB) WithLogger(logger func(*TB, string)) *TB {
	t.logger = logger
	return &t
}

func (t *TB) WithTimeout(timeout time.Duration) *TB {
	return t.BeforeEach(func(t2 *TB) *TB {
		ctx, cancel := context.WithTimeout(t2.Context(), timeout)
		t2.Cleanup(cancel)
		return t2.WithContext(ctx)
	})
}

// BeforeAll calls f immediately with itself and returns the result.
//
// It is not inherited by subtests.
func (t *TB) BeforeAll(f func(*TB) *TB) *TB {
	return f(t)
}

// BeforeEach configures f to run prior to each subtest.
func (t TB) BeforeEach(f Middleware) *TB {
	cpM := make([]Middleware, len(t.beforeEach))
	copy(cpM, t.beforeEach)
	cpM = append(cpM, f)
	t.beforeEach = cpM
	return &t
}

func (t *T) Run(name string, f func(context.Context, *T)) bool {
	return t.T.Run(name, func(testingT *testing.T) {
		sub := t.sub(name, testingT)
		for _, setup := range t.beforeEach {
			sub = setup(sub)
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
