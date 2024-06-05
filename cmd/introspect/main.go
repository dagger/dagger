package main

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/schema"
	"github.com/dagger/dagger/dagql"
)

func main() {
	ctx := context.Background()

	root := &core.Query{}
	dag := dagql.NewServer(root)
	coreMod := &schema.CoreMod{Dag: dag}
	if err := coreMod.Install(ctx, dag); err != nil {
		panic(err)
	}

	res, err := schema.SchemaIntrospectionJSON(ctx, dag)
	if err != nil {
		panic(err)
	}
	fmt.Println(string(res))
}
