package gql

import (
	"github.com/dagger/dagger/core/bbi"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/vektah/gqlparser/ast"
)

func init() {
	bbi.Register("gql", new(Driver))
}

type Driver struct{}

type Session struct {
	self dagql.Object
	srv  *dagql.Server
}

func (d Driver) NewSession(self dagql.Object, srv *dagql.Server) bbi.Session {
	return &Session{
		self: self,
		srv:  srv,
		IDs:  make(map[string]*call.ID),
	}
}

func (s *Session) Self() dagql.Object {
	return s.self
}

func (s *Session) Tools() []bbi.Tool {
	return []bbi.Tool{
		{
			Name: "dagger_version",
			Description: "Print the Dagger version.",
			Call: func(ctx context.Context, args any) (any, error) {
				return engine.Version, nil
			},
		},
		{
			Name: "learn_sdk",
			Description: "Learn how to convert a GraphQL query to code using a Dagger SDK.",
			Call: func(ctx context.Context, args any) (any, error) {
				sdk, ok := args.(map[string]any)["sdk"].(string)
				if !ok {
					return nil, fmt.Errorf("sdk must be a string")
				}
				switch strings.ToLower(sdk) {
				case "go", "golang":
					return knowledge.GoSDK, nil
				default:
					return nil, fmt.Errorf("unknown SDK: %s", sdk)
				}
			},
		},
		{
			Name: "learn_schema",
			Description: "Retrieve a snapshot of the current schema in GraphQL SDL format.",
			Call: func(ctx context.Context, args any) (any, error) {
				typeName, ok := args.(map[string]any)["type"].(string)
				if !ok {
					return nil, fmt.Errorf("type must be a string")
				}
				var resp introspection.Response
				if err := dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
					Query: introspection.Query,
				}, &graphql.Response{
					Data: &resp,
				}); err != nil {
					return nil, err
				}

				resp.Schema.OnlyType(typeName)

				var buf strings.Builder
				resp.Schema.RenderSDL(&buf)
				return buf.String(), nil
			},
		},


		//		{
		//			Name:        "gql-query",
		//			Description: "Send a graphql query to interact with your environment",
		//			Schema: map[string]any{
		//				"properties": map[string]any{
//					"q": map[string]string{
//						"description": "The graphql query to send",
//						"type": "string",
//					},
//					"save":
//				}
//			{
//  "properties": {
//    "path": {
//      "description": "Location of the directory to remove (e.g., \".github/\").",
//      "type": "string"
//    }
//  },
//  "required": [
//    "path"
//  ],
//  "type": "object"
//}
//			},
//		},
//	}
//}
//


	vars := map[string]any{}



	s.AddTool(
		mcp.NewTool("run_query",
			mcp.WithDescription(
				knowledge.Querying,
			),
			mcp.WithString("query",
				mcp.Required(),
				mcp.Description("The GraphQL query to execute."),
			),
			mcp.WithString("setVariable",
				mcp.Description("Assign the unrolled result value as a GraphQL variable for future queries."),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			query, ok := request.Params.Arguments["query"].(string)
			if !ok {
				return mcp.NewToolResultError("query must be a string"), nil
			}

			var resp graphql.Response
			if err := dag.GraphQLClient().MakeRequest(ctx, &graphql.Request{
				Query:     query,
				Variables: vars,
			}, &resp); err != nil {
				return nil, err
			}
			payload, err := json.Marshal(resp)
			if err != nil {
				return nil, err
			}

			if name, ok := request.Params.Arguments["setVariable"].(string); ok {
				val := unroll(resp.Data)
				slog.Info("setting variable", "name", name, "value", val)
				vars[name] = val
				return mcp.NewToolResultText("Variable defined: $" + name), nil
			}

			return mcp.NewToolResultText(string(payload)), nil
		})

	sseSrv := server.NewSSEServer(s, fmt.Sprintf("http://localhost:%s", PORT))
	if err := sseSrv.Start(fmt.Sprintf("0.0.0.0:%s", PORT)); err != nil {
		fmt.Printf("Server error: %v\n", err)
	}
}

func unroll(val any) any {
	if m, ok := val.(map[string]any); ok {
		for _, v := range m {
			return unroll(v)
		}
		return nil
	} else {
		return val
	}
}
