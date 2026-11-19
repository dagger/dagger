package dagql

import (
	"strings"
	"unsafe"

	"github.com/vektah/gqlparser/v2/ast"
)

// The GraphQL lexer cuts names and unescaped string literals out of the query
// text without copying them, so each shares the memory of the whole text. A
// selector keeps its field name and argument values for as long as its
// result is cached, and a query text holds every literal of the query: a
// cached call's short argument, such as an exec's command, would keep a
// sibling's large one, such as the exec's stdin, alive a second time, as part
// of the text. Selectors therefore take names from the schema and copy the
// literals they keep out of the text.

// astSource returns the query text pos points into, or "" if unknown.
func astSource(pos *ast.Position) string {
	if pos == nil || pos.Src == nil {
		return ""
	}
	return pos.Src.Input
}

// detachString returns s, or a copy of it when it is a slice of src.
func detachString(src, s string) string {
	if s == "" || src == "" {
		return s
	}
	start := uintptr(unsafe.Pointer(unsafe.StringData(src)))
	at := uintptr(unsafe.Pointer(unsafe.StringData(s)))
	if at < start || at >= start+uintptr(len(src)) {
		return s
	}
	return strings.Clone(s)
}

// detachArg returns val, an argument of type t as ast.Value.Value gives it,
// as detachValue does, except an ID or a list of IDs: an ID decodes into a
// new call ID without keeping its string, so copying it would only waste a
// copy of what is often the largest literal of a query.
func detachArg(src string, t *ast.Type, val any) any {
	for t != nil && t.Elem != nil {
		t = t.Elem
	}
	if t != nil && t.NamedType == "ID" {
		return val
	}
	return detachValue(src, val)
}

// detachValue returns v, an argument value as ast.Value.Value gives it, with
// every string that is a slice of src, object keys included, copied out of
// it.
func detachValue(src string, v any) any {
	if src == "" {
		return v
	}
	switch v := v.(type) {
	case string:
		return detachString(src, v)
	case []any:
		for i, elem := range v {
			v[i] = detachValue(src, elem)
		}
		return v
	case map[string]any:
		detached := make(map[string]any, len(v))
		for key, elem := range v {
			detached[detachString(src, key)] = detachValue(src, elem)
		}
		return detached
	default:
		return v
	}
}
