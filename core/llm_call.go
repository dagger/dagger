package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/slog"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"go.opentelemetry.io/otel/trace"
)

// buildSelector converts LLM args to a dagql.Selector for a single field.
func buildSelector(
	ctx context.Context,
	srv *dagql.Server,
	schema *ast.Schema,
	targetObjType dagql.ObjectType,
	fieldDef *ast.FieldDefinition,
	args map[string]any,
	objs *LLMObjects,
) (dagql.Selector, error) {
	sel := dagql.Selector{
		View:  srv.View,
		Field: fieldDef.Name,
	}
	field, ok := targetObjType.FieldSpec(fieldDef.Name, call.View(engine.Version))
	if !ok {
		return sel, fmt.Errorf("field %q not found in object type %q",
			fieldDef.Name,
			targetObjType.TypeName())
	}
	remainingArgs := make(map[string]any)
	maps.Copy(remainingArgs, args)
	delete(remainingArgs, "self")

	for _, arg := range field.Args.Inputs(srv.View) {
		if arg.Internal {
			continue
		}
		val, ok := args[arg.Name]
		if !ok {
			continue
		}
		delete(remainingArgs, arg.Name)
		argDef := fieldDef.Arguments.ForName(arg.Name)
		scalar, ok := srv.ScalarType(argDef.Type.Name())
		if !ok {
			return sel, fmt.Errorf("arg %q: unknown scalar type %q", arg.Name, argDef.Type.Name())
		}
		if idType, ok := dagql.UnwrapAs[dagql.IDType](scalar); ok {
			idStr, ok := val.(string)
			if ok {
				expectedType := strings.TrimSuffix(idType.TypeName(), "ID")
				envVal, err := objs.Lookup(ctx, srv, idStr, expectedType)
				if err != nil {
					return sel, fmt.Errorf("arg %q: %w", arg.Name, err)
				}
				obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](envVal)
				if !ok {
					return sel, fmt.Errorf("arg %q: expected object, got %T", arg.Name, envVal)
				}
				val = obj.ID()
			}
		}
		input, err := arg.Type.Decoder().DecodeInput(val)
		if err != nil {
			return sel, fmt.Errorf("arg %q: decode %T: %w", arg.Name, val, err)
		}
		sel.Args = append(sel.Args, dagql.NamedInput{
			Name:  arg.Name,
			Value: input,
		})
	}
	if len(remainingArgs) > 0 {
		return sel, fmt.Errorf("unknown args: %v", remainingArgs)
	}
	return sel, nil
}

// buildConstructorSelector builds a dagql.Selector for constructing a module's
// entrypoint object from Query.
func buildConstructorSelector(
	srv *dagql.Server,
	schema *ast.Schema,
	autoConstruct *ObjectTypeDef,
) (dagql.Selector, error) {
	consFieldDef := schema.Query.Fields.ForName(gqlFieldName(autoConstruct.Name))
	if consFieldDef == nil {
		return dagql.Selector{}, fmt.Errorf("constructor field %q not found on Query", gqlFieldName(autoConstruct.Name))
	}
	return dagql.Selector{
		View:  srv.View,
		Field: consFieldDef.Name,
	}, nil
}

// execute runs a chain of selectors against a target, handling sync.
func execute(
	ctx context.Context,
	srv *dagql.Server,
	target dagql.AnyObjectResult,
	sels ...dagql.Selector,
) (dagql.AnyResult, error) {
	var val dagql.AnyResult
	if err := srv.Select(ctx, target, &val, sels...); err != nil {
		return nil, err
	}
	if id, ok := dagql.UnwrapAs[dagql.IDType](val); ok {
		syncedObj, err := srv.Load(ctx, id.ID())
		if err != nil {
			return nil, fmt.Errorf("failed to load synced object: %w", err)
		}
		val = syncedObj
	}
	return val, nil
}

// appendSyncSelector adds a "sync" selector if the return type supports it.
func appendSyncSelector(srv *dagql.Server, fieldDef *ast.FieldDefinition, sels []dagql.Selector) []dagql.Selector {
	if retObjType, ok := srv.ObjectType(fieldDef.Type.NamedType); ok {
		if sync, ok := retObjType.FieldSpec("sync", srv.View); ok {
			sels = append(sels, dagql.Selector{
				View:  srv.View,
				Field: sync.Name,
			})
		}
	}
	return sels
}

// formatResult converts a dagql result into an LLM-friendly string.
// moduleName is non-empty for auto-constructed module tools, triggering
// builder-pattern handling (suppress response when return type == moduleName).
func formatResult(
	ctx context.Context,
	srv *dagql.Server,
	objs *LLMObjects,
	val dagql.AnyResult,
	moduleName string,
) (string, error) {
	if changes, ok := dagql.UnwrapAs[dagql.ObjectResult[*Changeset]](val); ok {
		return summarizePatch(ctx, srv, changes)
	}

	if moduleName != "" {
		if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](val); ok {
			llmID := objs.Track(obj, "")
			if obj.Type().Name() == moduleName {
				return "", nil
			}
			return toolObjectResponse(ctx, srv, objs, obj, llmID)
		}
	}

	return outputToLLM(ctx, srv, objs, val)
}

// outputToLLM converts a dagql result to a string for the LLM.
func outputToLLM(ctx context.Context, srv *dagql.Server, objs *LLMObjects, val dagql.Typed) (string, error) {
	if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](val); ok {
		return toolObjectResponse(ctx, srv, objs, obj, objs.Track(obj, ""))
	}

	result, err := sanitizeResult(objs, val)
	if err != nil {
		return "", fmt.Errorf("failed to simplify result: %w", err)
	}

	if str, ok := result.(string); ok {
		return str, nil
	}

	if result == nil {
		return "", nil
	}

	return toolStructuredResponse(map[string]any{
		"result": result,
	})
}

// sanitizeResult converts a dagql result into a JSON-safe value.
func sanitizeResult(objs *LLMObjects, val dagql.Typed) (any, error) {
	if obj, ok := dagql.UnwrapAs[dagql.AnyObjectResult](val); ok {
		return objs.Track(obj, ""), nil
	}

	if anyRes, ok := dagql.UnwrapAs[dagql.AnyResult](val); ok {
		return sanitizeResult(objs, anyRes.Unwrap())
	}

	if list, ok := dagql.UnwrapAs[dagql.Enumerable](val); ok {
		var res []any
		for i := 1; i <= list.Len(); i++ {
			val, err := list.Nth(i)
			if err != nil {
				return nil, fmt.Errorf("failed to get ID for object %d: %w", i, err)
			}
			simpl, err := sanitizeResult(objs, val)
			if err != nil {
				return nil, fmt.Errorf("failed to simplify list element %d: %w", i, err)
			}
			res = append(res, simpl)
		}
		return res, nil
	}

	if str, ok := dagql.UnwrapAs[dagql.String](val); ok {
		bytes := []byte(str.String())
		if !utf8.Valid(bytes) {
			return map[string]any{
				"type":   "non-utf8-string",
				"bytes":  len(bytes),
				"digest": digest.FromBytes(bytes),
			}, nil
		}
		return str.String(), nil
	}

	if val == (Void{}) {
		return nil, nil
	}

	return val, nil
}

// toolObjectResponse formats an object result with its trivial fields.
func toolObjectResponse(ctx context.Context, srv *dagql.Server, objs *LLMObjects, target dagql.AnyObjectResult, objID string) (string, error) {
	schema := srv.Schema()
	typeName := target.Type().Name()
	known := objs.HasType(typeName)
	res := map[string]any{
		"result": objID,
	}
	data := map[string]any{}
	for _, field := range schema.Types[typeName].Fields {
		trivial := field.Directives.ForName(trivialFieldDirectiveName) != nil
		if !trivial {
			continue
		}
		val, err := target.Select(ctx, srv, dagql.Selector{
			View:  srv.View,
			Field: field.Name,
		})
		if err != nil {
			return "", err
		}
		if _, isObj := srv.ObjectType(val.Type().Name()); isObj {
			continue
		}
		datum, err := sanitizeResult(objs, val)
		if err != nil {
			return "", err
		}
		data[field.Name] = datum
	}
	if len(data) > 0 {
		res["data"] = data
	}
	if !known {
		res["hint"] = fmt.Sprintf("New methods available for type %q.", typeName)
	}
	return toolStructuredResponse(res)
}

// prependLogs captures logs from the span and prepends them to the result.
func prependLogs(ctx context.Context, res *string) {
	spanID := trace.SpanContextFromContext(ctx).SpanID()
	logs, err := captureLogs(ctx, spanID.String())
	if err != nil {
		slog.Error("failed to capture logs", "error", err)
	} else if len(logs) > 0 {
		logs = limitLines(spanID.String(), logs, llmLogsLastLines, llmLogsMaxLineLen)
		*res = strings.Trim(strings.Join(logs, "\n")+"\n\n"+*res, "\n")
	}
}

// toolStructuredResponse formats a value as indented JSON.
func toolStructuredResponse(val any) (string, error) {
	str := new(strings.Builder)
	enc := json.NewEncoder(str)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(val); err != nil {
		return "", fmt.Errorf("failed to encode response %T: %w", val, err)
	}
	return str.String(), nil
}

// toolErrorMessage formats an error for LLM consumption.
func toolErrorMessage(err error) string {
	errResponse := err.Error()
	var extErr dagql.ExtendedError
	if errors.As(err, &extErr) {
		var exts []string
		for k, v := range extErr.Extensions() {
			if k == "traceparent" || k == "baggage" {
				continue
			}
			var ext strings.Builder
			fmt.Fprintf(&ext, "<%s>\n", k)
			switch v := v.(type) {
			case string:
				ext.WriteString(v)
			default:
				jsonBytes, err := json.Marshal(v)
				if err != nil {
					fmt.Fprintf(&ext, "error marshalling value: %s", err.Error())
				} else {
					ext.Write(jsonBytes)
				}
			}
			fmt.Fprintf(&ext, "\n</%s>", k)
			exts = append(exts, ext.String())
		}
		if len(exts) > 0 {
			sort.Strings(exts)
			errResponse += "\n\n" + strings.Join(exts, "\n\n")
		}
	}
	return errResponse
}
