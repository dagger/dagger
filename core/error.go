package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

type Error struct {
	Message string        `field:"true" doc:"A description of the error."`
	Values  []*ErrorValue `field:"true" doc:"The extensions of the error."`
}

func NewError(message string) *Error {
	return &Error{
		Message: message,
	}
}

func NewErrorFromErr(ctx context.Context, fromErr error) (objErr dagql.ObjectResult[*Error], outerErr error) {
	sels := []dagql.Selector{}
	var extErr dagql.ExtendedError
	if errors.As(fromErr, &extErr) {
		sels = append(sels, dagql.Selector{
			Field: "error",
			Args: []dagql.NamedInput{
				{
					Name:  "message",
					Value: dagql.String(extErr.Error()),
				},
			},
		})
		exts := extErr.Extensions()
		for _, k := range slices.Sorted(maps.Keys(exts)) {
			val, err := json.Marshal(exts[k])
			if err != nil {
				fmt.Println("failed to marshal error value:", err)
			}
			sels = append(sels, dagql.Selector{
				Field: "withValue",
				Args: []dagql.NamedInput{
					{
						Name:  "name",
						Value: dagql.String(k),
					},
					{
						Name:  "value",
						Value: JSON(val),
					},
				},
			})
		}
	} else {
		sels = append(sels, dagql.Selector{
			Field: "error",
			Args: []dagql.NamedInput{
				{
					Name:  "message",
					Value: dagql.String(fromErr.Error()),
				},
			},
		})
	}
	srv, srvErr := CurrentDagqlServer(ctx)
	if srvErr != nil {
		return objErr, srvErr
	}
	if selErr := srv.Select(ctx, srv.Root(), &objErr, sels...); selErr != nil {
		return objErr, selErr
	}
	return objErr, nil
}

func (e *Error) Clone() *Error {
	cp := *e
	cp.Values = slices.Clone(e.Values)
	return &cp
}

func (e *Error) WithValue(name string, value JSON) *Error {
	cp := e.Clone()
	cp.Values = append(cp.Values, &ErrorValue{
		Name:  name,
		Value: value,
	})
	return cp
}

func (e *Error) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Error",
		NonNull:   true,
	}
}

var _ error = (*Error)(nil)

func (e *Error) Error() string {
	return e.Message
}

var _ dagql.ExtendedError = (*Error)(nil)

func (e *Error) Extensions() map[string]any {
	ext := map[string]any{}
	for _, v := range e.Values {
		var val any
		json.Unmarshal(v.Value, &val)
		ext[v.Name] = val
	}
	return ext
}

type ErrorValue struct {
	Name  string `field:"true" doc:"The name of the value."`
	Value JSON   `field:"true" doc:"The value."`
}

func (e *ErrorValue) Type() *ast.Type {
	return &ast.Type{
		NamedType: "ErrorValue",
		NonNull:   true,
	}
}
