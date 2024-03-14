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
	coreModDeps := core.NewModDeps(root, []core.Mod{coreMod})
	if err := coreMod.Install(ctx, dag); err != nil {
		panic(err)
	}

	res, err := coreModDeps.SchemaIntrospectionJSON(ctx, false)
	if err != nil {
		panic(err)
	}
	fmt.Println(res)
}
