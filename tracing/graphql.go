package tracing

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/idproto"
	"github.com/dagger/dagger/dagql/ioctx"
	"github.com/vito/progrock"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"google.golang.org/protobuf/types/known/anypb"
)

func AroundFunc(ctx context.Context, self dagql.Object, id *idproto.ID, next func(context.Context) (dagql.Typed, error)) func(context.Context) (dagql.Typed, error) {
	// install tracing at the outermost layer so we don't ignore perf impact of
	// other telemetry
	return SpanAroundFunc(ctx, self, id,
		ProgrockAroundFunc(ctx, self, id, next))
}

func SpanAroundFunc(ctx context.Context, self dagql.Object, id *idproto.ID, next func(context.Context) (dagql.Typed, error)) func(context.Context) (dagql.Typed, error) {
	return func(ctx context.Context) (dagql.Typed, error) {
		if isIntrospection(id) {
			return next(ctx)
		}

		ctx, span := Tracer.Start(ctx, id.Display())

		v, err := next(ctx)

		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		if v != nil {
			serialized, err := json.MarshalIndent(v, "", "  ")
			if err == nil {
				// span.AddEvent("event", trace.WithAttributes(attribute.String("value", string(serialized))))
				span.SetAttributes(attribute.String("value", string(serialized)))
			}
		}
		span.End()

		return v, err
	}
}

func ProgrockAroundFunc(ctx context.Context, self dagql.Object, id *idproto.ID, next func(context.Context) (dagql.Typed, error)) func(context.Context) (dagql.Typed, error) {
	return func(ctx context.Context) (dagql.Typed, error) {
		if isIntrospection(id) {
			return next(ctx)
		}

		rec := progrock.FromContext(ctx)

		dig, err := id.Digest()
		if err != nil {
			slog.Warn("failed to digest id", "id", id.Display(), "err", err)
			return next(ctx)
		}
		// TODO: we don't need this for anything yet
		// inputs, err := id.Inputs()
		// if err != nil {
		// 	slog.Warn("failed to digest inputs", "id", id.Display(), "err", err)
		// 	return next(ctx)
		// }
		vtx := rec.Vertex(dig, id.Field,
			// progrock.WithInputs(inputs...),
			// TODO: these really shouldn't be internal, but for backwards
			// compatibility we don't want to overwhelm the TUI with a bunch of
			// vertices.
			progrock.Internal())
		ctx = ioctx.WithStdout(ctx, vtx.Stdout())
		ctx = ioctx.WithStderr(ctx, vtx.Stderr())

		payload, err := anypb.New(id)
		if err != nil {
			slog.Warn("failed to anypb.New(id)", "id", id.Display(), "err", err)
			return next(ctx)
		}
		// send ID payload to the frontend
		vtx.Meta("id", payload)

		// respect user-configured pipelines
		if w, ok := self.(dagql.Wrapper); ok {
			if pl, ok := w.Unwrap().(pipeline.Pipelineable); ok {
				rec = pl.PipelinePath().RecorderGroup(rec)
			}
		}

		// group any self-calls or Buildkit vertices beneath this vertex
		rec = rec.WithParent(dig.String())

		// call the resolver with progrock wired up
		ctx = progrock.ToContext(ctx, rec)

		res, resolveErr := next(ctx)

		if resolveErr != nil {
			// NB: we do +id.Display() instead of setting it as a field to avoid
			// dobule quoting
			slog.Warn("error resolving "+id.Display(), "error", resolveErr)
		}

		// record an object result as an output of this vertex
		//
		// this allows the UI to "simplify" this ID back to its creator ID when it
		// sees it in the future if it wants to, e.g. showing mymod.unit().stdout()
		// instead of the full container().from().[...].stdout() ID
		if obj, ok := res.(dagql.Object); ok {
			objDigest, err := obj.ID().Digest()
			if err != nil {
				slog.Error("failed to digest object", "id", id.Display(), "err", err)
			} else {
				vtx.Output(objDigest)
			}
		}

		vtx.Done(resolveErr)

		return res, resolveErr
	}
}

// isIntrospection detects whether an ID is an introspection query.
//
// These queries tend to be very large and are not interesting for users to
// see.
func isIntrospection(id *idproto.ID) bool {
	if id.Base == nil {
		switch id.Field {
		case "__schema",
			"currentTypeDefs",
			"currentFunctionCall",
			"currentModule":
			return true
		default:
			return false
		}
	} else {
		return isIntrospection(id.Base)
	}
}
