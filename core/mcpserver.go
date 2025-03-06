package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"slices"
	"strconv"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/moby/buildkit/util/bklog"
)

func (llm *LLM) MCP(ctx context.Context, dag *dagql.Server) error {
	bklog.G(ctx).Debugf("🎃 Starting MCP function")
	s := mcpserver.NewMCPServer("Dagger", "0.0.1")
	s.AddTool(
		mcp.NewTool("toto",
			mcp.WithDescription("A tool to greet someone."),
			mcp.WithString("name", mcp.Required(), mcp.Description("Name of the person to greet")),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			name, ok := request.Params.Arguments["name"].(string)
			if !ok {
				return nil, errors.New("name must be a string")
			}
			return mcp.NewToolResultText(fmt.Sprintf("Hello %s!", name)), nil
		})

	bk, err := llm.Query.Buildkit(ctx)
	if err != nil {
		return fmt.Errorf("buildkit client error: %w", err)
	}

	// FIXME: do not use Terminal attachable
	term, err := bk.OpenTerminal(ctx)
	if err != nil {
		return fmt.Errorf("open terminal error: %w", err)
	}
	defer term.Close(bkgwpb.UnknownExitStatus)
	bklog.G(ctx).Debugf("🎃 Terminal opened")
	go func() {
		for {
			select {
			case <-term.ResizeCh:
			case err := <-term.ErrCh:
				bklog.G(ctx).Debugf("🎃 error: |%+v|", err)
				return
			}
		}
	}()

	// Read from Stdin in a goroutine
	go io.Copy(term.Stdout, term.Stdin)

	select {
	case <-ctx.Done():
	case <-time.After(30 * time.Second):
		bklog.G(ctx).Warnf("🎃 Timeout waiting for Stdin input")
		return fmt.Errorf("timeout waiting for stdin input")
	}

	term.Close(0) // clean exit
	return ctx.Err()
}
