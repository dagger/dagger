package pipes

import (
	"context"
	"fmt"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/dagql"
	"github.com/vito/dagql/ioctx"
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
				Channel: make(chan dagql.String, args.Buffer.Value),
			}, nil
		}),
	}.Install(srv)

	dagql.Fields[Pipe]{
		dagql.Func("read", func(ctx context.Context, self Pipe, _ any) (dagql.String, error) {
			fmt.Fprintln(ioctx.Stdout(ctx), "reading from", self.Channel)
			return <-self.Channel, nil
		}).Impure(),
		dagql.Func("write", func(ctx context.Context, self Pipe, args struct {
			Message dagql.String
		}) (Pipe, error) {
			fmt.Fprintln(ioctx.Stdout(ctx), "writing", args.Message, "to", self.Channel)
			self.Channel <- args.Message
			return self, nil
		}).Impure(),
	}.Install(srv)

}
