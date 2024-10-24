package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dagger/dagger/cmd/codegen/introspection"
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

	var schemaResp introspection.Response
	if err := json.Unmarshal([]byte(res), &schemaResp); err != nil {
		panic(fmt.Errorf("failed to unmarshal introspection JSON: %w", err))
	}

	schemaTypes := introspection.Types{}
	for _, schemaType := range schemaResp.Schema.Types {
		if strings.HasPrefix(schemaType.Name, "_") {
			continue
		}
		schemaTypes = append(schemaTypes, schemaType)
	}
	schemaResp.Schema.Types = schemaTypes

	res, err = json.MarshalIndent(schemaResp, "", "  ")
	if err != nil {
		panic(fmt.Errorf("failed to marshal introspection JSON: %w", err))
	}

	fmt.Println(string(res))
}
