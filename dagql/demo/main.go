package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/dagql"
	"github.com/vito/dagql/idproto"
	"github.com/vito/dagql/internal/pipes"
	"github.com/vito/dagql/internal/points"
	"github.com/vito/dagql/introspection"
	"github.com/vito/dagql/ioctx"
	"github.com/vito/progrock"
)

type Query struct {
}

func (Query) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Query",
		NonNull:   true,
	}
}

func (Query) TypeDefinition() *ast.Definition {
	return &ast.Definition{
		Kind: ast.Object,
		Name: "Query",
	}
}

func main() {
	ctx := context.Background()
	tape := progrock.NewTape()
	rec := progrock.NewRecorder(tape)
	ctx = progrock.ToContext(ctx, rec)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := dagql.NewServer(Query{})
	srv.Around(TelemetryFunc(rec))
	points.Install[Query](srv)
	pipes.Install[Query](srv)
	introspection.Install[Query](srv)

	http.Handle("/", playground.Handler("GraphQL playground", "/query"))
	http.Handle("/query", handler.NewDefaultServer(srv))

	l, err := net.Listen("tcp", ":"+port)
	if err != nil {
		panic(err)
	}
	defer l.Close()

	log.Fatal(progrock.DefaultUI().Run(ctx, tape, func(ctx context.Context, ui progrock.UIClient) (err error) {
		vtx := rec.Vertex("dagql", "server")
		fmt.Fprintf(vtx.Stdout(), "connect to http://localhost:%s for GraphQL playground", port)
		defer vtx.Done(err)
		go func() {
			<-ctx.Done()
			l.Close()
		}()
		return http.Serve(l, nil)
	}))
}

func TelemetryFunc(rec *progrock.Recorder) dagql.AroundFunc {
	return func(
		ctx context.Context,
		obj dagql.Object,
		id *idproto.ID,
		next func(context.Context) (dagql.Typed, error),
	) func(context.Context) (dagql.Typed, error) {
		dig, err := id.Digest()
		if err != nil {
			slog.Error("failed to digest id", "error", err, "id", id.Display())
			return next
		}
		return func(context.Context) (dagql.Typed, error) {
			vtx := rec.Vertex(dig, id.Display())
			ctx = ioctx.WithStdout(ctx, vtx.Stdout())
			ctx = ioctx.WithStderr(ctx, vtx.Stderr())
			res, err := next(ctx)
			vtx.Done(err)
			return res, err
		}
	}
}
