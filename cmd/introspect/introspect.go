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
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/cache"
	"github.com/spf13/cobra"
)

func Introspect(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()

	resp, err := getIntrospection(ctx)
	if err != nil {
		return err
	}
	res, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return err
	}

	fmt.Println(string(res))
	return nil
}

func getIntrospection(ctx context.Context) (*introspection.Response, error) {
	root := &core.Query{}
	baseCache, err := cache.NewCache[string, dagql.AnyResult](ctx, "")
	if err != nil {
		return nil, err
	}
	dag := dagql.NewServer(root, dagql.NewSessionCache(baseCache))
	dag.View = call.View(version)
	coreMod := &schema.CoreMod{Dag: dag}
	if err := coreMod.Install(ctx, dag); err != nil {
		return nil, err
	}

	res, err := schema.SchemaIntrospectionJSON(ctx, dag)
	if err != nil {
		return nil, err
	}

	var schemaResp introspection.Response
	if err := json.Unmarshal([]byte(res), &schemaResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal introspection JSON: %w", err)
	}

	schemaTypes := introspection.Types{}
	for _, schemaType := range schemaResp.Schema.Types {
		if strings.HasPrefix(schemaType.Name, "_") {
			continue
		}
		schemaTypes = append(schemaTypes, schemaType)
	}
	schemaResp.Schema.Types = schemaTypes

	return &schemaResp, nil
}
