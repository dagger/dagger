package core

import (
	"encoding/json"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

type Error struct {
	Query *Query

	Message string        `field:"true" doc:"A description of the error."`
	Values  []*ErrorValue `field:"true" doc:"The extensions of the error."`
}

func NewError(message string) *Error {
	return &Error{
		Message: message,
	}
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
