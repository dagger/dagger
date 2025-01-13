package core

import "github.com/vektah/gqlparser/v2/ast"

type Message struct {
	Message  string `field:"true"`
	Markdown bool   `field:"true"`
	Level    string `field:"true"`
}

func (l *Message) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Message",
		NonNull:   true,
	}
}
