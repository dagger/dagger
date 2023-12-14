package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"

	"github.com/99designs/gqlgen/graphql/handler"
	"github.com/99designs/gqlgen/graphql/playground"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/dagql"
	"github.com/vito/dagql/internal/pipes"
	"github.com/vito/dagql/internal/points"
	"github.com/vito/dagql/introspection"
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

func (Query) Definition() *ast.Definition {
	return &ast.Definition{
		Kind: ast.Object,
		Name: "Query",
	}
}

func main() {
	ctx := context.Background()
	tape := progrock.NewTape()
	tape.ShowAllOutput(true)
	rec := progrock.NewRecorder(tape)
	ctx = progrock.ToContext(ctx, rec)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := dagql.NewServer(Query{})
	srv.RecordTo(rec)
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
