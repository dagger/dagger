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

func Run(ctx context.Context, testingT *testing.T, suite any, middleware ...Middleware) {
	t := New(ctx, testingT)
	for _, m := range middleware {
		t = m(t)
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
			testingT.Fatalf("suite method %s does not have the right signature; must be func(testctx.T); have %T",
				methT.Name,
				methV.Interface())
		}

		t.Run(methT.Name, tf)
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

func (t T) WithLogger(logger func(*T, string)) *T {
	t.logger = logger
	return &t
}

func (t *T) WithTimeout(timeout time.Duration) *T {
	return t.BeforeEach(func(t2 *T) *T {
		ctx, cancel := context.WithTimeout(t2.Context(), timeout)
		t2.Cleanup(cancel)
		return t2.WithContext(ctx)
	})
}

// BeforeAll calls f immediately with itself and returns the result.
//
// It is not inherited by subtests.
func (t *T) BeforeAll(f func(*T) *T) *T {
	return f(t)
}

// BeforeEach configures f to run prior to each subtest.
func (t T) BeforeEach(f Middleware) *T {
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

func (t *T) Log(vals ...any) {
	t.log(fmt.Sprintln(vals...))
	t.T.Log(vals...)
}

func (t *T) Logf(format string, vals ...any) {
	t.logf(format, vals...)
	t.T.Logf(format, vals...)
}

func (t *T) Error(vals ...any) {
	msg := fmt.Sprint(vals...)
	t.errors = append(t.errors, msg)
	t.log(msg)
	t.T.Error(vals...)
}

func (t *T) Errorf(format string, vals ...any) {
	t.logf(format, vals...)
	t.errors = append(t.errors, fmt.Sprintf(format, vals...))
	t.T.Errorf(format, vals...)
}

func (t *T) Fatal(vals ...any) {
	t.log(fmt.Sprintln(vals...))
	t.errors = append(t.errors, fmt.Sprint(vals...))
	t.T.Fatal(vals...)
}

func (t *T) Fatalf(format string, vals ...any) {
	t.logf(format, vals...)
	t.errors = append(t.errors, fmt.Sprintf(format, vals...))
	t.T.Fatalf(format, vals...)
}

func (t *T) Skip(vals ...any) {
	t.log(fmt.Sprintln(vals...))
	t.T.Skip(vals...)
}

func (t *T) Skipf(format string, vals ...any) {
	t.logf(format, vals...)
	t.T.Skipf(format, vals...)
}

func (t *T) Errors() string {
	return strings.Join(t.errors, "\n")
}

func (t *T) log(out string) {
	if t.logger != nil {
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		t.logger(t, out)
		return
	}
}

func (t *T) logf(format string, vals ...any) {
	if t.logger != nil {
		out := fmt.Sprintf(format, vals...)
		if !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		t.logger(t, out)
		return
	}
}
