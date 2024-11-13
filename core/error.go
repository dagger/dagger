package core

import "github.com/vektah/gqlparser/v2/ast"

type Error struct {
	Query *Query `json:"-"`

	Message string `field:"true" doc:"A description of the error."`

	// Stack    string `field:"true" doc:"The stack trace of the error."`
	// Source   *File  `field:"true" doc:"The source file where the error occurred."`
	// Line     int    `field:"true" doc:"The line in the source file where the error occurred."`
	// Original *Error `field:"true" doc:"The original error that caused this error."`

	// what about screenshots and stuff?
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
