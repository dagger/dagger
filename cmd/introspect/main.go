package main

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core/schema"
)

func main() {
	ctx := context.Background()
	srv, err := schema.New(ctx, schema.InitializeArgs{})
	if err != nil {
		panic(err)
	}
	res, err := srv.Introspect(ctx)
	if err != nil {
		panic(err)
	}
	fmt.Println(res)
}
