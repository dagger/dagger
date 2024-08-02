package testctx

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

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

	t.runSubtests()
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

	parent     *T
	subtestIdx int

	ctx        context.Context
	baseName   string
	logger     func(*T, string)
	beforeEach []func(*T) *T
	afterSelf  []func(*T)
	subtests   []*Subtest
	errors     []string
}

func (t *T) AfterSelf(f func(*T)) {
	t.afterSelf = append(t.afterSelf, f)
}

type Subtest struct {
	Parent   *T
	Index    int
	BaseName string
	F        func(context.Context, *T)
}

func (t *T) id() string {
	if t.parent != nil {
		return fmt.Sprintf("%s:%d", t.parent.Name(), t.subtestIdx)
	} else {
		// should never actually be the case, since all methods are already subtests
		return t.baseName
	}
}

func (st *Subtest) id() string {
	return fmt.Sprintf("%s:%d", st.Parent.Name(), st.Index)
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

func (t *T) Run(name string, f func(context.Context, *T)) {
	t.subtests = append(t.subtests, &Subtest{
		Parent:   t,
		Index:    len(t.subtests),
		BaseName: name,
		F:        f,
	})
}

func (t *T) runSubtests() {
	for _, f := range t.afterSelf {
		f(t)
	}
	for _, subTest := range t.subtests {
		t.T.Run(subTest.BaseName, func(testingT *testing.T) {
			subT := t.sub(subTest.BaseName, testingT)
			subT.subtestIdx = subTest.Index
			for _, setup := range t.beforeEach {
				subT = setup(subT)
			}
			subTest.F(subT.Context(), subT)
			subT.runSubtests()
		})
	}
}

func (t *T) sub(name string, testingT *testing.T) *T {
	sub := New(t.ctx, testingT)
	sub.parent = t
	sub.baseName = name
	sub.logger = t.logger
	sub.beforeEach = t.beforeEach
	// don't copy t.afterSelf or t.subtests
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
