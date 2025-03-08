package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/moby/buildkit/util/bklog"
)

func (llm *LLM) MCP(ctx context.Context, dag *dagql.Server) error {
	bklog.G(ctx).Debugf("🎃 Starting MCP function")

	// Create MCP server with tool
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
		},
	)

	// Get buildkit client
	bk, err := llm.Query.Buildkit(ctx)
	if err != nil {
		return fmt.Errorf("buildkit client error: %w", err)
	}

	pc, err := bk.OpenPipe(ctx)
	if err != nil {
		return fmt.Errorf("open pipe error: %w", err)
	}
	defer pc.Close()
	bklog.G(ctx).Debugf("🎃 Pipe opened")

	// Create a context with cancel to coordinate goroutines
	ctxWithCancel, cancel := context.WithCancel(ctx)
	defer cancel()

	// Create channels for coordination
	errCh := make(chan error, 2)

	// Create a buffer to accumulate JSON data
	var jsonBuffer bytes.Buffer

	// Create pipes for communication
	stdinR, stdinW := io.Pipe()

	// Start a goroutine to read from terminal and accumulate JSON
	go func() {
		defer stdinW.Close()

		buf := make([]byte, 4096)
		for {
			select {
			case <-ctxWithCancel.Done():
				return
			default:
				n, err := pc.Stdin.Read(buf)
				if err != nil {
					if !errors.Is(err, io.EOF) {
						bklog.G(ctx).Warnf("pipe recv err: %v", err)
						errCh <- err
					}
					return
				}

				if n > 0 {
					bklog.G(ctxWithCancel).Debugf("🎃 Read %d bytes from term.Stdin: %q", n, buf[:n])

					// Add data to buffer
					jsonBuffer.Write(buf[:n])

					bklog.G(ctxWithCancel).Debugf("🎃 after write %d\n", n)
					// Try to extract complete JSON objects
					for {
						bklog.G(ctxWithCancel).Debugf("🎃 inside loop\n")
						// Look for a complete JSON object
						data_json := jsonBuffer.Bytes()

						// Find opening and closing braces
						openBrace := bytes.IndexByte(data_json, '{')
						if openBrace == -1 {
							break // No JSON object start found
						}

						// Count braces to find matching closing brace
						braceCount := 0
						closingIndex := -1

						for i := openBrace; i < len(data_json); i++ {
							if data_json[i] == '{' {
								braceCount++
							} else if data_json[i] == '}' {
								braceCount--
								if braceCount == 0 {
									closingIndex = i
									break
								}
							}
						}

						if closingIndex == -1 {
							break // No complete JSON object found
						}

						// Extract the complete JSON object
						jsonObj := data_json[openBrace : closingIndex+1]
						bklog.G(ctxWithCancel).Debugf("🎃 Found complete JSON: %s", jsonObj)

						// Write the complete JSON object to the pipe with a newline
						if _, err := fmt.Fprintf(stdinW, "%s\n", jsonObj); err != nil {
							bklog.G(ctxWithCancel).Errorf("🎃 Error writing to pipe: %v", err)
							return
						}

						bklog.G(ctxWithCancel).Debugf("🎃 before next\n")
						// Remove the processed JSON from the buffer
						jsonBuffer.Next(closingIndex + 1)
						bklog.G(ctxWithCancel).Debugf("🎃 after next\n")
					}
					bklog.G(ctxWithCancel).Debugf("🎃 exited loop\n")
				}
			}
		}
	}()

	// Create a writer that logs responses
	responseWriter := &responseWriterWithLogging{
		writer: pc.Stdout,
		ctx:    ctxWithCancel,
	}

	// Create MCP server
	srv := mcpserver.NewStdioServer(s)

	// Use standard log package
	logger := stdlog.New(bklog.G(ctxWithCancel).Writer(), "", 0)
	srv.SetErrorLogger(logger)

	// Add a ping mechanism to verify data flow
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				bklog.G(ctxWithCancel).Debugf("🎃 Checking if MCP server is responsive...")
			case <-ctxWithCancel.Done():
				return
			}
		}
	}()

	// Start MCP server in a goroutine
	go func() {
		bklog.G(ctxWithCancel).Debugf("🎃 Starting MCP server listener")
		err := srv.Listen(ctxWithCancel, stdinR, responseWriter)
		if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
			bklog.G(ctxWithCancel).Errorf("🎃 MCP server error: %v", err)
			errCh <- fmt.Errorf("MCP server error: %w", err)
		}
	}()

	// Wait for events
	select {
	case <-ctx.Done():
		bklog.G(ctx).Debugf("🎃 Context done, shutting down")
		return ctx.Err()
	case <-time.After(120 * time.Second): // Increased timeout for testing
		bklog.G(ctx).Warnf("🎃 Timeout waiting for Stdin input")
		return fmt.Errorf("timeout waiting for stdin input")
	case err := <-errCh:
		bklog.G(ctx).Errorf("🎃 Error in goroutine: %v", err)
		return err
	case err := <-pc.ErrCh:
		bklog.G(ctx).Errorf("🎃 Error in pipe client: %v", err)
		return err
	}
}

// Custom writer that logs what's being written
type responseWriterWithLogging struct {
	writer io.Writer
	ctx    context.Context
}

func (w *responseWriterWithLogging) Write(p []byte) (n int, err error) {
	bklog.G(w.ctx).Debugf("🎃 Writing response: %q", string(p))
	return w.writer.Write(p)
}
