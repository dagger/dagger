package pipes

import (
	"context"
	"fmt"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/ioctx"
)

type Pipe struct {
	Channel chan dagql.String
}

func (Pipe) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Pipe",
		NonNull:   true,
	}
}

func Install[Root dagql.Typed](srv *dagql.Server) {
	dagql.Fields[Root]{
		dagql.Func("pipe", func(ctx context.Context, self Root, args struct {
			Buffer dagql.Int `default:"0"`
		}) (Pipe, error) {
			return Pipe{
				Channel: make(chan dagql.String, args.Buffer.Int()),
			}, nil
		}),
	}.Install(srv)

	dagql.Fields[Pipe]{
		dagql.Func("read", func(ctx context.Context, self Pipe, _ struct{}) (dagql.String, error) {
			fmt.Fprintln(ioctx.Stdout(ctx), "reading from", self.Channel)
			return <-self.Channel, nil
		}).Impure("Reads a value from internal state."),
		dagql.Func("write", func(ctx context.Context, self Pipe, args struct {
			Message dagql.String
		}) (Pipe, error) {
			fmt.Fprintln(ioctx.Stdout(ctx), "writing", args.Message, "to", self.Channel)
			self.Channel <- args.Message
			return self, nil
		}).Impure("Writes a value to internal state."),
	}.Install(srv)
}
