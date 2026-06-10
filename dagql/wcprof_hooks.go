package dagql

import (
	"context"
	"errors"

	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/wcprof"
)

// Wall-clock profiling (wcprof) hooks for the dagql cache. These are no-ops
// unless profiling is enabled; call sites gate on wcprof.Active() before
// computing class strings so the disabled path stays an atomic load.

// profCallClass derives the operation class for a call frame, e.g.
// "Container.withExec" or "myModule:Foo.bar" for module-provided fields.
func profCallClass(frame *ResultCall) string {
	if frame == nil {
		return "?"
	}
	field := frame.Field
	if field == "" {
		field = frame.SyntheticOp
	}
	if field == "" {
		field = "?"
	}
	recv := "Query"
	if frame.Receiver != nil {
		recv = ""
		if frame.Receiver.Call != nil {
			recv = profNamedType(frame.Receiver.Call.Type)
		}
		if recv == "" && frame.Receiver.shared != nil {
			if rc := frame.Receiver.shared.loadResultCall(); rc != nil {
				recv = profNamedType(rc.Type)
			}
		}
		if recv == "" {
			recv = "?"
		}
	}
	if frame.Module != nil && frame.Module.Name != "" {
		return frame.Module.Name + ":" + recv + "." + field
	}
	return recv + "." + field
}

func profNamedType(typ *ResultCallType) string {
	for typ != nil {
		if typ.NamedType != "" {
			return typ.NamedType
		}
		typ = typ.Elem
	}
	return ""
}

func profClientID(ctx context.Context) string {
	if md, err := engine.ClientMetadataFromContext(ctx); err == nil {
		return md.ClientID
	}
	return ""
}

func profResultID(res AnyResult) uint64 {
	if res == nil {
		return 0
	}
	shared := res.cacheSharedResult()
	if shared == nil {
		return 0
	}
	return uint64(shared.id)
}

func profErrOutcome(err error) wcprof.Outcome {
	switch {
	case err == nil:
		return wcprof.OutcomeOK
	case errors.Is(err, context.Canceled):
		return wcprof.OutcomeCanceled
	default:
		return wcprof.OutcomeError
	}
}
