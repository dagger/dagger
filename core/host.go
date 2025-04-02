package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
)

type Host struct {
	Query *Query
}

func (*Host) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Host",
		NonNull:   true,
	}
}

func (*Host) TypeDescription() string {
	return "Information about the host environment."
}

func (*Host) FromJSON(ctx context.Context, bs []byte) (dagql.Typed, error) {
	query, ok := QueryFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("failed to get query from context")
	}

	var x Host
	if err := json.Unmarshal(bs, &x); err != nil {
		return nil, err
	}
	x.Query = query
	return &x, nil
}
