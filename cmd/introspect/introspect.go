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
	"github.com/dagger/dagger/engine"
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
	ctx = engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{
		ClientID:  "dagql-introspect",
		SessionID: "dagql-introspect",
	})

	root := &core.Query{}
	baseCache, err := dagql.NewCache(ctx, "", nil)
	if err != nil {
		return nil, err
	}
	ctx = dagql.ContextWithCache(ctx, baseCache)
	coreSchemaBase, err := schema.NewCoreSchemaBase(ctx, nil)
	if err != nil {
		return nil, err
	}
	dag, err := coreSchemaBase.Fork(ctx, root, call.View(version))
	if err != nil {
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
