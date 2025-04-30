package schema

import (
	"context"
	"encoding/json"
	"fmt"

	"golang.org/x/mod/semver"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/introspection"
	"github.com/dagger/dagger/engine/buildkit"
)

type SchemaResolvers interface {
	Install()
}

type Evaluatable interface {
	dagql.Typed
	Evaluate(context.Context) (*buildkit.Result, error)
}

func Syncer[T Evaluatable]() dagql.Field[T] {
	return dagql.NodeFunc("sync", func(ctx context.Context, self dagql.Instance[T], _ struct{}) (dagql.ID[T], error) {
		_, err := self.Self.Evaluate(ctx)
		if err != nil {
			var zero dagql.ID[T]
			return zero, err
		}
		return dagql.NewID[T](self.ID()), nil
	})
}

func collectInputsSlice[T dagql.Type](inputs []dagql.InputObject[T]) []T {
	ts := make([]T, len(inputs))
	for i, input := range inputs {
		ts[i] = input.Value
	}
	return ts
}

func collectIDInstances[T dagql.Typed](ctx context.Context, srv *dagql.Server, ids []dagql.ID[T]) ([]dagql.Instance[T], error) {
	ts := make([]dagql.Instance[T], len(ids))
	for i, id := range ids {
		inst, err := id.Load(ctx, srv)
		if err != nil {
			return nil, err
		}
		ts[i] = inst
	}
	return ts, nil
}

func asArrayInput[T any, I dagql.Input](ts []T, conv func(T) I) dagql.ArrayInput[I] {
	ins := make(dagql.ArrayInput[I], len(ts))
	for i, v := range ts {
		ins[i] = conv(v)
	}
	return ins
}

func SchemaIntrospectionJSON(ctx context.Context, dag *dagql.Server) (json.RawMessage, error) {
	data, err := dag.Query(ctx, introspection.Query, nil)
	if err != nil {
		return nil, fmt.Errorf("introspection query failed: %w", err)
	}
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal introspection result: %w", err)
	}
	return json.RawMessage(jsonBytes), nil
}

// AllVersion is a view that contains all versions.
var AllVersion = dagql.AllView{}

// AfterVersion is a view that checks if a target version is greater than *or*
// equal to the filtered version.
type AfterVersion string

var _ dagql.ViewFilter = AfterVersion("")

func (minVersion AfterVersion) Contains(version dagql.View) bool {
	if version == "" {
		return true
	}
	return semver.Compare(string(version), string(minVersion)) >= 0
}

// BeforeVersion is a view that checks if a target version is less than the
// filtered version.
type BeforeVersion string

var _ dagql.ViewFilter = BeforeVersion("")

func (maxVersion BeforeVersion) Contains(version dagql.View) bool {
	if version == "" {
		return false
	}
	return semver.Compare(string(version), string(maxVersion)) < 0
}

func ptr[T any](v T) *T {
	return &v
}
