package testctx

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
)

type Middleware = func(*T) *T

func WithParallel(t *T) *T {
	t.Parallel()
	return t.
		BeforeEach(func(t *T) *T {
			t.Parallel()
			return t
		})
}

func Run(ctx context.Context, t *testing.T, suite any, middleware ...Middleware) {
	tc := New(ctx, t)
	for _, m := range middleware {
		tc = m(tc)
	}

	suiteT := reflect.TypeOf(suite)
	suiteV := reflect.ValueOf(suite)

	for i := 0; i < suiteV.NumMethod(); i++ {
		methT := suiteT.Method(i)
		if !strings.HasPrefix(methT.Name, "Test") {
			continue
		}

		methV := suiteV.Method(i)
		tf, ok := methV.Interface().(func(context.Context, *T))
		if !ok {
			t.Fatalf("suite method %s does not have the right signature; must be func(testctx.T); have %T",
				methT.Name,
				methV.Interface())
		}

		tc.Run(methT.Name, tf)
	}
}

func WithOTelTracing(tracer trace.Tracer) Middleware {
	wrapSpan := func(t *T) *T {
		ctx, span := tracer.Start(t.Context(), t.BaseName())
		t.Cleanup(func() {
			if t.Failed() {
				span.SetStatus(codes.Error, "test failed")
			}
			span.End()
		})
		return t.WithContext(ctx)
	}
	return func(t *T) *T {
		return t.
			BeforeAll(wrapSpan).
			BeforeEach(wrapSpan)
	}
}

func WithOTelLogging(logger log.Logger) Middleware {
	return func(t *T) *T {
		return t.WithLogger(func(t2 *T, msg string) {
			var rec log.Record
			rec.SetBody(log.StringValue(msg))
			rec.SetTimestamp(time.Now())
			logger.Emit(t2.Context(), rec)
		})
	}
}

func Combine(middleware ...Middleware) Middleware {
	return func(t *T) *T {
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
		T:   t,
		ctx: ctx,
	}
}

type T struct {
	*testing.T

	ctx        context.Context
	baseName   string
	logger     func(*T, string)
	beforeEach []func(*T) *T
	errors     []string
}

func (t *T) BaseName() string {
	if t.baseName != "" {
		return t.baseName
	}
	return t.Name()
}

func (t *T) Context() context.Context {
	return t.ctx
}

func (t T) WithContext(ctx context.Context) *T {
	t.ctx = ctx
	return &t
}

func (tc T) WithLogger(logger func(*T, string)) *T {
	tc.logger = logger
	return &tc
}

func (t *T) WithTimeout(timeout time.Duration) *T {
	return t.BeforeEach(func(t *T) *T {
		ctx, cancel := context.WithTimeout(t.Context(), timeout)
		t.Cleanup(cancel)
		return t.WithContext(ctx)
	})
}

// BeforeAll calls f immediately with itself and returns the result.
//
// It is not inherited by subtests.
func (tc *T) BeforeAll(f func(*T) *T) *T {
	return f(tc)
}

// BeforeEach configures f to run prior to each subtest.
func (tc T) BeforeEach(f Middleware) *T {
	cpM := make([]Middleware, len(tc.beforeEach))
	copy(cpM, tc.beforeEach)
	cpM = append(cpM, f)
	tc.beforeEach = cpM
	return &tc
}

func (tc *T) Run(name string, f func(context.Context, *T)) bool {
	return tc.T.Run(name, func(t *testing.T) {
		sub := tc.sub(name, t)
		for _, setup := range tc.beforeEach {
			sub = setup(sub)
		}
		f(sub.Context(), sub)
	})
}

func (tc *T) sub(name string, t *testing.T) *T {
	sub := New(tc.ctx, t)
	sub.baseName = name
	sub.logger = tc.logger
	sub.beforeEach = tc.beforeEach
	return sub
}

func (tc *T) Log(vals ...any) {
	tc.log(fmt.Sprintln(vals...))
	tc.T.Log(vals...)
}

func (tc *T) Logf(format string, vals ...any) {
	tc.logf(format, vals...)
	tc.T.Logf(format, vals...)
}

func (tc *T) Error(vals ...any) {
	msg := fmt.Sprint(vals...)
	tc.errors = append(tc.errors, msg)
	tc.log(msg)
	tc.T.Error(vals...)
}

func (tc *T) Errorf(format string, vals ...any) {
	tc.logf(format, vals...)
	tc.errors = append(tc.errors, fmt.Sprintf(format, vals...))
	tc.T.Errorf(format, vals...)
}

func (tc *T) Fatal(vals ...any) {
	tc.log(fmt.Sprintln(vals...))
	tc.errors = append(tc.errors, fmt.Sprint(vals...))
	tc.T.Fatal(vals...)
}

func (tc *T) Fatalf(format string, vals ...any) {
	tc.logf(format, vals...)
	tc.errors = append(tc.errors, fmt.Sprintf(format, vals...))
	tc.T.Fatalf(format, vals...)
}

func (tc *T) Skip(vals ...any) {
	tc.log(fmt.Sprintln(vals...))
	tc.T.Skip(vals...)
}

func (tc *T) Skipf(format string, vals ...any) {
	tc.logf(format, vals...)
	tc.T.Skipf(format, vals...)
}

func (tc *T) Errors() string {
	return strings.Join(tc.errors, "\n")
}

func (tc *T) log(out string) {
	if tc.logger != nil {
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		tc.logger(tc, out)
		return
	}
}

func (tc *T) logf(format string, vals ...any) {
	if tc.logger != nil {
		out := fmt.Sprintf(format, vals...)
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		tc.logger(tc, out)
		return
	}
}
