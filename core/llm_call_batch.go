package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/patchpreview"
	"github.com/jedevc/diffparser"
	"github.com/muesli/termenv"
	"github.com/sourcegraph/conc/pool"
	"go.opentelemetry.io/otel/codes"
	"slices"

	telemetry "github.com/dagger/otel-go"
)

// CallBatch executes a batch of tool calls, handling MCP server syncing efficiently by
// grouping calls by destructiveness and server to avoid workspace conflicts
func (m *MCP) CallBatch(ctx context.Context, tools []LLMTool, toolCalls []*LLMToolCall, toolCallDisplays map[string]toolCallDisplay) []*LLMMessage {
	readOnlyMCPCalls := make(map[string][]*LLMToolCall)
	destructiveMCPCalls := make(map[string][]*LLMToolCall)
	regularCalls := make([]*LLMToolCall, 0)
	destructiveCalls := make([]*LLMToolCall, 0)

	for _, toolCall := range toolCalls {
		tool, err := m.LookupTool(toolCall.Name, tools)
		if err != nil {
			regularCalls = append(regularCalls, toolCall)
			continue
		}

		if tool.Server == "" {
			if tool.ReadOnly {
				regularCalls = append(regularCalls, toolCall)
			} else {
				destructiveCalls = append(destructiveCalls, toolCall)
			}
			continue
		}

		if tool.ReadOnly {
			readOnlyMCPCalls[tool.Server] = append(readOnlyMCPCalls[tool.Server], toolCall)
		} else {
			destructiveMCPCalls[tool.Server] = append(destructiveMCPCalls[tool.Server], toolCall)
		}
	}

	var allResults []*LLMMessage

	callCtx := func(callID string) context.Context {
		if tc, ok := toolCallDisplays[callID]; ok {
			return tc.Ctx
		}
		return ctx
	}

	endToolCallSpan := func(callID string, errored bool, errMsg string) {
		if tc, ok := toolCallDisplays[callID]; ok {
			if errored {
				tc.Span.SetStatus(codes.Error, errMsg)
			}
			tc.Span.End()
		}
	}

	// 1. Execute destructive non-MCP calls sequentially
	for _, call := range destructiveCalls {
		result, isError := m.Call(callCtx(call.CallID), tools, call)
		endToolCallSpan(call.CallID, isError, result)
		allResults = append(allResults, &LLMMessage{
			Role: LLMMessageRoleUser,
			Content: []*LLMContentBlock{{
				Kind:    LLMContentToolResult,
				Text:    result,
				CallID:  call.CallID,
				Errored: isError,
			}},
		})
	}

	// 2. Execute destructive MCP calls one server at a time
	for serverName, calls := range destructiveMCPCalls {
		serverResults := m.callBatchMCPServer(ctx, tools, calls, serverName, toolCallDisplays)
		allResults = append(allResults, serverResults...)
	}

	// 3. Execute all regular read-only (non-MCP) calls in parallel
	if len(regularCalls) > 0 {
		allResults = append(allResults, m.callBatchRegular(ctx, tools, regularCalls, toolCallDisplays)...)
	}

	// 4. Execute all read-only MCP calls in parallel
	var readOnlyToolCalls []*LLMToolCall
	for _, calls := range readOnlyMCPCalls {
		readOnlyToolCalls = append(readOnlyToolCalls, calls...)
	}
	if len(readOnlyToolCalls) > 0 {
		allResults = append(allResults, m.callBatchRegular(ctx, tools, readOnlyToolCalls, toolCallDisplays)...)
	}

	return allResults
}

// callBatchMCPServer executes a batch of calls for a single MCP server with proper workspace syncing
func (m *MCP) callBatchMCPServer(ctx context.Context, tools []LLMTool, toolCalls []*LLMToolCall, serverName string, toolCallDisplays map[string]toolCallDisplay) []*LLMMessage {
	mcpCfg, ok := m.mcpServers[serverName]
	if !ok {
		return m.callBatchRegular(ctx, tools, toolCalls, toolCallDisplays)
	}

	if m.env.ID() == nil {
		return m.callBatchRegular(ctx, tools, toolCalls, toolCallDisplays)
	}

	ctr := mcpCfg.Service.Self().Container
	if ctr.Config.WorkingDir == "" || ctr.Config.WorkingDir == "/" {
		return m.callBatchRegular(ctx, tools, toolCalls, toolCallDisplays)
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return m.callBatchRegular(ctx, tools, toolCalls, toolCallDisplays)
	}
	mcps, err := query.MCPClients(ctx)
	if err != nil {
		return m.callBatchRegular(ctx, tools, toolCalls, toolCallDisplays)
	}
	sess, err := mcps.Dial(ctx, mcpCfg)
	if err != nil {
		return m.callBatchRegular(ctx, tools, toolCalls, toolCallDisplays)
	}

	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return m.callBatchRegular(ctx, tools, toolCalls, toolCallDisplays)
	}
	var wsDir dagql.ObjectResult[*Directory]
	if err := srv.Select(ctx, m.env.Self().Workspace, &wsDir, dagql.Selector{
		Field: "directory",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString(".")},
		},
	}); err != nil {
		return m.callBatchRegular(ctx, tools, toolCalls, toolCallDisplays)
	}

	var results []*LLMMessage
	_, _, err = mcpCfg.Service.Self().runAndSnapshotChanges(
		ctx,
		sess.ID(),
		ctr.Config.WorkingDir,
		wsDir.Self(),
		func() error {
			results = m.callBatchRegular(ctx, tools, toolCalls, toolCallDisplays)
			return nil
		})

	if err != nil {
		return m.callBatchRegular(ctx, tools, toolCalls, toolCallDisplays)
	}

	return results
}

// callBatchRegular is the original parallel execution logic without MCP-specific syncing
func (m *MCP) callBatchRegular(ctx context.Context, tools []LLMTool, toolCalls []*LLMToolCall, toolCallDisplays map[string]toolCallDisplay) []*LLMMessage {
	toolCallsPool := pool.NewWithResults[*LLMMessage]()
	for _, toolCall := range toolCalls {
		toolCallsPool.Go(func() *LLMMessage {
			callCtx := ctx
			if tc, ok := toolCallDisplays[toolCall.CallID]; ok {
				callCtx = tc.Ctx
			}
			content, isError := m.Call(callCtx, tools, toolCall)
			if tc, ok := toolCallDisplays[toolCall.CallID]; ok {
				if isError {
					tc.Span.SetStatus(codes.Error, content)
				}
				tc.Span.End()
			}
			return &LLMMessage{
				Role: LLMMessageRoleUser,
				Content: []*LLMContentBlock{{
					Kind:    LLMContentToolResult,
					Text:    content,
					CallID:  toolCall.CallID,
					Errored: isError,
				}},
			}
		})
	}
	return toolCallsPool.Wait()
}

// summarizePatch generates a human-readable summary of a changeset.
func summarizePatch(ctx context.Context, srv *dagql.Server, changes dagql.ObjectResult[*Changeset]) (string, error) {
	var rawPatch string
	if err := srv.Select(ctx, changes, &rawPatch, dagql.Selector{
		View:  srv.View,
		Field: "asPatch",
	}, dagql.Selector{
		View:  srv.View,
		Field: "contents",
	}); err != nil {
		return fmt.Sprintf("WARNING: failed to fetch patch summary: %s", err), nil
	}
	if rawPatch == "" {
		return "", nil
	}
	if strings.Count(rawPatch, "\n") > 100 {
		var addedPaths, removedPaths []string
		if err := srv.Select(ctx, changes, &addedPaths, dagql.Selector{
			View:  srv.View,
			Field: "addedPaths",
		}); err != nil {
			return fmt.Sprintf("WARNING: failed to fetch added paths: %s", err), nil
		}
		if err := srv.Select(ctx, changes, &removedPaths, dagql.Selector{
			View:  srv.View,
			Field: "removedPaths",
		}); err != nil {
			return fmt.Sprintf("WARNING: failed to fetch removed paths: %s", err), nil
		}
		addedDirectories := slices.DeleteFunc(addedPaths, func(s string) bool {
			return !strings.HasSuffix(s, "/")
		})
		removedDirectories := slices.DeleteFunc(removedPaths, func(s string) bool {
			return !strings.HasSuffix(s, "/")
		})
		patch, err := diffparser.Parse(rawPatch)
		if err != nil {
			return "", fmt.Errorf("parse patch: %w", err)
		}
		preview := &patchpreview.PatchPreview{
			Patch:       patch,
			AddedDirs:   addedDirectories,
			RemovedDirs: removedDirectories,
		}
		var res strings.Builder
		llmOut := termenv.NewOutput(&res, termenv.WithProfile(termenv.Ascii))
		if err := preview.Summarize(llmOut, 80); err != nil {
			return fmt.Sprintf("WARNING: failed to render patch summary: %s", err), nil
		}
		return res.String(), nil
	}
	return rawPatch, nil
}

// toAny converts a value to map[string]any via JSON round-trip.
func toAny(v any) (res map[string]any, rerr error) {
	pl, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return res, json.Unmarshal(pl, &res)
}

// Call executes a single tool call.
func (m *MCP) Call(ctx context.Context, tools []LLMTool, toolCall *LLMToolCall) (res string, failed bool) {
	tool, err := m.LookupTool(toolCall.Name, tools)
	if err != nil {
		return err.Error(), true
	}

	var args map[string]any
	if err := json.Unmarshal(toolCall.Arguments, &args); err != nil {
		return fmt.Sprintf("failed to parse tool arguments: %s", err), true
	}

	stdio := telemetry.SpanStdio(ctx, InstrumentationLibrary)
	defer stdio.Close()
	defer func() {
		fmt.Fprintln(stdio.Stdout, res)
	}()

	result, err := tool.Call(ctx, args)
	if err != nil {
		return toolErrorMessage(err), true
	}

	switch v := result.(type) {
	case string:
		return v, false
	default:
		jsonBytes, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("Failed to marshal result: %s", err), true
		}
		return string(jsonBytes), false
	}
}
