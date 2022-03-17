package zerolog

import (
	"context"
)

var disabledLogger *Logger

func init() {
	SetGlobalLevel(TraceLevel)
	l := Nop()
	disabledLogger = &l
}

type ctxKey struct{}

// WithContext returns a copy of ctx with l associated. If an instance of Logger
// is already in the context, the context is not updated.
//
// For instance, to add a field to an existing logger in the context, use this
// notation:
//
//     ctx := r.Context()
//     l := zerolog.Ctx(ctx)
//     l.UpdateContext(func(c Context) Context {
//         return c.Str("bar", "baz")
//     })
func (l *Logger) WithContext(ctx context.Context) context.Context {
	if lp, ok := ctx.Value(ctxKey{}).(*Logger); ok {
		if lp == l {
			// Do not store same logger.
			return ctx
		}
	} else if l.level == Disabled {
		// Do not store disabled logger.
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, l)
}

// Ctx returns the Logger associated with the ctx. If no logger
// is associated, DefaultContextLogger is returned, unless DefaultContextLogger
// is nil, in which case a disabled logger is returned.
func Ctx(ctx context.Context) *Logger {
	if l, ok := ctx.Value(ctxKey{}).(*Logger); ok {
		return l
	} else if l = DefaultContextLogger; l != nil {
		return l
	}
	return disabledLogger
}
