package core

import (
	"context"
	"encoding/json"
	"net"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcptransport "github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

type testLLMToolSource struct {
	mu        sync.Mutex
	toolLoads int
	called    chan any
}

func (s *testLLMToolSource) DefaultSystemPrompt(context.Context) (string, error) {
	return "test instructions", nil
}

func (s *testLLMToolSource) Tools(context.Context) ([]LLMTool, error) {
	s.mu.Lock()
	s.toolLoads++
	s.mu.Unlock()

	return []LLMTool{
		{
			Name:        "echo",
			Description: "Echo a message.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message": map[string]any{"type": "string"},
				},
				"required": []string{"message"},
			},
			Call: func(_ context.Context, args any) (any, error) {
				s.called <- args
				return map[string]any{"echo": args.(map[string]any)["message"]}, nil
			},
		},
		{
			Name: "internal_id",
			Call: func(context.Context, any) (any, error) {
				return "hidden", nil
			},
		},
	}, nil
}

func (s *testLLMToolSource) loadCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.toolLoads
}

func newTestLLMToolServer(t *testing.T) (*LLMToolServer, *testLLMToolSource) {
	t.Helper()
	source := &testLLMToolSource{called: make(chan any, 1)}
	server, err := newLLMToolServer(t.Context(), source)
	require.NoError(t, err)
	return server, source
}

func TestLLMToolServerStreamableHTTP(t *testing.T) {
	server, source := newTestLLMToolServer(t)
	httpServer := httptest.NewServer(server.StreamableHTTPHandler())
	t.Cleanup(httpServer.Close)

	transport, err := mcptransport.NewStreamableHTTP(httpServer.URL)
	require.NoError(t, err)
	client := mcpclient.NewClient(transport)
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	require.NoError(t, client.Start(t.Context()))
	initialized, err := client.Initialize(t.Context(), mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ClientInfo: mcp.Implementation{Name: "dagger-test", Version: "1.0.0"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "Dagger", initialized.ServerInfo.Name)
	require.Equal(t, "test instructions", initialized.Instructions)

	listed, err := client.ListTools(t.Context(), mcp.ListToolsRequest{})
	require.NoError(t, err)
	require.Len(t, listed.Tools, 1)
	require.Equal(t, "echo", listed.Tools[0].Name)

	result, err := client.CallTool(t.Context(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      "echo",
			Arguments: map[string]any{"message": "hello"},
		},
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Len(t, result.Content, 1)
	text, ok := result.Content[0].(mcp.TextContent)
	require.Truef(t, ok, "unexpected MCP content type %T", result.Content[0])
	require.JSONEq(t, `{"echo":"hello"}`, text.Text)
	require.Equal(t, map[string]any{"message": "hello"}, <-source.called)
	require.Equal(t, 2, source.loadCount(), "tools should refresh after a successful call")
}

func TestLLMToolServerServeStdio(t *testing.T) {
	server, _ := newTestLLMToolServer(t)
	serverPipe, clientPipe := net.Pipe()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.ServeStdio(t.Context(), serverPipe)
	}()

	require.NoError(t, clientPipe.SetDeadline(time.Now().Add(5*time.Second)))
	require.NoError(t, json.NewEncoder(clientPipe).Encode(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": mcp.LATEST_PROTOCOL_VERSION,
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "dagger-stdio-test",
				"version": "1.0.0",
			},
		},
	}))

	var response struct {
		ID     int `json:"id"`
		Result struct {
			ServerInfo   mcp.Implementation `json:"serverInfo"`
			Instructions string             `json:"instructions"`
		} `json:"result"`
		Error *mcp.JSONRPCError `json:"error"`
	}
	require.NoError(t, json.NewDecoder(clientPipe).Decode(&response))
	require.Nil(t, response.Error)
	require.Equal(t, 1, response.ID)
	require.Equal(t, "Dagger", response.Result.ServerInfo.Name)
	require.Equal(t, "test instructions", response.Result.Instructions)

	require.NoError(t, clientPipe.Close())
	require.NoError(t, <-serveErr)
}
