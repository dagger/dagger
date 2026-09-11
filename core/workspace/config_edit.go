package workspace

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/creachadair/tomledit/parser"
	"github.com/creachadair/tomledit/scanner"
	toml "github.com/pelletier/go-toml"
)

// configText retains the original bytes. The parser supplies logical keys;
// the scanner supplies byte ranges. Neither is used to format the document.
// Re-indexing after each edit keeps offsets local to one immutable input.
type configText struct {
	data       []byte
	statements []configStatement
}

type configStatement struct {
	path                 []string
	section              []string
	start, end           int
	valueStart, valueEnd int
	table, arrayTable    bool
}

type configToken struct {
	kind       scanner.Token
	start, end int
}

func configTokens(data []byte) ([]configToken, error) {
	sc := scanner.New(bytes.NewReader(data))
	var tokens []configToken
	for {
		err := sc.Next()
		if err == io.EOF {
			return tokens, nil
		}
		if err != nil {
			return nil, err
		}
		span := sc.Span()
		tokens = append(tokens, configToken{sc.Token(), span.Pos, span.End})
	}
}

func parseConfigText(data []byte) (*configText, error) {
	tokens, err := configTokens(data)
	if err != nil {
		return nil, fmt.Errorf("scan config: %w", err)
	}
	doc := &configText{data: data}
	var section []string
	start, depth := -1, 0
	flush := func(end int) error {
		if start < 0 {
			return nil
		}
		first := tokens[start]
		last := tokens[end-1]
		items, err := parser.New(bytes.NewReader(data[first.start:last.end])).Items()
		if err != nil {
			return fmt.Errorf("parse config statement: %w", err)
		}
		if len(items) != 1 {
			return fmt.Errorf("expected one config statement, got %d", len(items))
		}
		stmt := configStatement{start: configLineStart(data, first.start), end: last.end}
		switch item := items[0].(type) {
		case *parser.Heading:
			section = slices.Clone(item.Name)
			stmt.path, stmt.table, stmt.arrayTable = section, true, item.IsArray
		case *parser.KeyValue:
			stmt.path = append(slices.Clone(section), item.Name...)
			stmt.section = slices.Clone(section)
			for i := start; i < end; i++ {
				if tokens[i].kind == scanner.Equal {
					stmt.valueStart = tokens[i+1].start
					break
				}
			}
			for i := end - 1; i >= start; i-- {
				if tokens[i].kind != scanner.Comment && tokens[i].kind != scanner.Newline {
					stmt.valueEnd = tokens[i].end
					break
				}
			}
		default:
			return fmt.Errorf("unsupported config statement %T", item)
		}
		doc.statements = append(doc.statements, stmt)
		start = -1
		return nil
	}
	for i, token := range tokens {
		if start < 0 {
			if token.kind == scanner.Comment || token.kind == scanner.Newline {
				continue
			}
			start = i
		}
		switch token.kind {
		case scanner.LBracket, scanner.LInline:
			depth++
		case scanner.RBracket, scanner.RInline:
			depth--
		}
		// Comment tokens include their terminating newline.
		if depth == 0 && (token.kind == scanner.Newline || token.kind == scanner.Comment) {
			if err := flush(i + 1); err != nil {
				return nil, err
			}
		}
	}
	if err := flush(len(tokens)); err != nil {
		return nil, err
	}
	return doc, nil
}

func configLineStart(data []byte, offset int) int {
	return bytes.LastIndexByte(data[:offset], '\n') + 1
}

func configNewline(data []byte) string {
	if i := bytes.IndexByte(data, '\n'); i > 0 && data[i-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

func configPathPrefix(path, prefix []string) bool {
	return len(path) >= len(prefix) && slices.Equal(path[:len(prefix)], prefix)
}

func replaceConfigText(data []byte, start, end int, replacement string) []byte {
	out := make([]byte, 0, len(data)-(end-start)+len(replacement))
	out = append(out, data[:start]...)
	out = append(out, replacement...)
	return append(out, data[end:]...)
}

func (doc *configText) set(parts []string, value any) ([]byte, error) {
	replaceTable := false
	for _, stmt := range doc.statements {
		if stmt.arrayTable && configPathPrefix(parts, stmt.path) {
			return nil, fmt.Errorf("cannot edit %q inside an array of tables", JoinConfigPath(parts...))
		}
		if stmt.table {
			if configPathPrefix(stmt.path, parts) {
				replaceTable = true
			}
			continue
		}
		if slices.Equal(stmt.path, parts) {
			rendered, err := renderConfigEdit(value, doc.data[stmt.valueStart:stmt.valueEnd])
			if err != nil {
				return nil, err
			}
			return replaceConfigText(doc.data, stmt.valueStart, stmt.valueEnd, rendered), nil
		}
		if configPathPrefix(parts, stmt.path) {
			return doc.editInline(stmt, parts[len(stmt.path):], value, false)
		}
		if configPathPrefix(stmt.path, parts) {
			replaceTable = true
		}
	}
	if replaceTable {
		updated, err := doc.remove(parts)
		if err != nil {
			return nil, err
		}
		doc, err = parseConfigText(updated)
		if err != nil {
			return nil, err
		}
		return doc.set(parts, value)
	}

	// Insert into an existing table, after its last assignment and before
	// comments belonging to the next table. New tables are appended.
	parent := parts[:len(parts)-1]
	writtenKey := parts[len(parts)-1:]
	position, found := 0, len(parent) == 0
	for _, stmt := range doc.statements {
		if stmt.table && slices.Equal(stmt.path, parent) {
			position, found = stmt.end, true
		}
		if !stmt.table && slices.Equal(stmt.path[:len(stmt.path)-1], parent) {
			position, found = stmt.end, true
			writtenKey = parts[len(stmt.section):]
		}
	}
	rendered, err := renderConfigEdit(value, nil)
	if err != nil {
		return nil, err
	}
	nl := configNewline(doc.data)
	line := JoinConfigPath(writtenKey...) + " = " + rendered + nl
	if !found {
		return doc.appendTable(parent, line), nil
	}
	if position == 0 && len(parent) == 0 {
		// Keep leading comments at the beginning of the file.
		position = len(doc.data)
		if len(doc.statements) > 0 {
			position = doc.statements[0].start
		}
	}
	if position > 0 && doc.data[position-1] != '\n' {
		line = nl + line
	}
	return replaceConfigText(doc.data, position, position, line), nil
}

func (doc *configText) appendTable(parts []string, body string) []byte {
	nl := configNewline(doc.data)
	prefix := ""
	if len(doc.data) > 0 {
		if doc.data[len(doc.data)-1] != '\n' {
			prefix += nl
		}
		if !bytes.HasSuffix(doc.data, []byte(nl+nl)) {
			prefix += nl
		}
	}
	return replaceConfigText(doc.data, len(doc.data), len(doc.data), prefix+"["+JoinConfigPath(parts...)+"]"+nl+body)
}

func (doc *configText) ensureTable(parts []string) ([]byte, error) {
	for _, stmt := range doc.statements {
		if configPathPrefix(stmt.path, parts) {
			return doc.data, nil
		}
		if !stmt.table && configPathPrefix(parts, stmt.path) {
			tree, err := toml.LoadBytes(doc.data)
			if err != nil {
				return nil, err
			}
			if tree.GetPath(parts) != nil {
				return doc.data, nil
			}
			return doc.set(parts, map[string]any{})
		}
	}
	return doc.appendTable(parts, ""), nil
}

func (doc *configText) remove(parts []string) ([]byte, error) {
	out := doc.data
	for i := len(doc.statements) - 1; i >= 0; i-- {
		stmt := doc.statements[i]
		if configPathPrefix(stmt.path, parts) {
			out = replaceConfigText(out, stmt.start, stmt.end, "")
		} else if !stmt.table && configPathPrefix(parts, stmt.path) {
			return doc.editInline(stmt, parts[len(stmt.path):], nil, true)
		}
	}
	return out, nil
}

// Inline tables are edited through a temporary table body. Only the resulting
// value is spliced back; sibling fields retain their original bytes.
func (doc *configText) editInline(stmt configStatement, parts []string, value any, remove bool) ([]byte, error) {
	raw := doc.data[stmt.valueStart:stmt.valueEnd]
	rendered, err := editInlineConfigValue(raw, parts, value, remove)
	if err != nil {
		return nil, err
	}
	return replaceConfigText(doc.data, stmt.valueStart, stmt.valueEnd, rendered), nil
}

func editInlineConfigValue(raw []byte, parts []string, value any, remove bool) (string, error) {
	tokens, err := configTokens(raw)
	if err != nil {
		return "", err
	}
	if len(tokens) < 2 || tokens[0].kind != scanner.LInline {
		return "", fmt.Errorf("cannot edit %q inside a scalar config value", JoinConfigPath(parts...))
	}
	// Split on commas belonging to this table, not to nested values.
	start, depth := 1, 0
	for i := 1; i < len(tokens); i++ {
		tok := tokens[i]
		if depth == 0 && (tok.kind == scanner.Comma || tok.kind == scanner.RInline) {
			if i > start {
				first, last := tokens[start].start, tokens[i-1].end
				child, err := parseConfigText(raw[first:last])
				if err != nil {
					return "", err
				}
				if len(child.statements) != 1 {
					return "", fmt.Errorf("invalid inline config entry")
				}
				entry := child.statements[0]
				if configPathPrefix(parts, entry.path) || remove && configPathPrefix(entry.path, parts) {
					if remove && configPathPrefix(entry.path, parts) {
						if tok.kind == scanner.Comma {
							last = tok.end
						} else if start > 1 {
							first = tokens[start-1].start
						}
						updated := replaceConfigText(raw, first, last, "")
						// Dotted keys can define several entries under the
						// removed table in one inline value.
						return editInlineConfigValue(updated, parts, nil, true)
					}
					var updated []byte
					if remove {
						updated, err = child.remove(parts)
					} else {
						updated, err = child.set(parts, value)
					}
					if err != nil {
						return "", err
					}
					return string(replaceConfigText(raw, tokens[start].start, tokens[i-1].end, string(updated))), nil
				}
			}
			start = i + 1
		}
		switch tok.kind {
		case scanner.LBracket, scanner.LInline:
			depth++
		case scanner.RBracket, scanner.RInline:
			depth--
		}
	}
	if remove {
		return string(raw), nil
	}
	rendered, err := renderConfigEdit(value, nil)
	if err != nil {
		return "", err
	}
	separator := ""
	if len(tokens) > 2 {
		separator = ", "
	}
	at := tokens[len(tokens)-1].start
	return string(replaceConfigText(raw, at, at, separator+JoinConfigPath(parts...)+" = "+rendered)), nil
}

func renderConfigEdit(value any, old []byte) (string, error) {
	// Compare decoded values so []string and []any, and integer widths, have
	// the same meaning. An unchanged value retains even unusual literal forms.
	plain, err := renderConfigLiteral(value)
	if err != nil {
		return "", err
	}
	if len(old) > 0 {
		before, beforeErr := toml.LoadBytes(append([]byte("v = "), old...))
		after, afterErr := toml.Load("v = " + plain)
		if beforeErr == nil && afterErr == nil && configValuesEqual(before.Get("v"), after.Get("v")) {
			return string(old), nil
		}
	}
	if text, ok := value.(string); ok && len(old) > 0 {
		candidate := ""
		switch {
		case bytes.HasPrefix(old, []byte("'''")) && !strings.Contains(text, "'''"):
			candidate = "'''" + configStringOpeningNewline(old) + text + "'''"
		case bytes.HasPrefix(old, []byte("\"\"\"")):
			candidate = "\"\"\"" + configStringOpeningNewline(old) + string(scanner.EscapeMultiline(text)) + "\"\"\""
		case old[0] == '\'' && !strings.ContainsRune(text, '\''):
			candidate = "'" + text + "'"
		}
		if candidate != "" {
			// Literal strings cannot contain every control character, and
			// multiline strings trim an initial newline. Keep the style only
			// if it represents the requested value without changing it.
			if _, err := parser.ParseValue(candidate); err == nil {
				if decoded, err := toml.Load("v = " + candidate); err == nil && decoded.Get("v") == text {
					return candidate, nil
				}
			}
		}
	}
	v := reflect.ValueOf(value)
	if v.IsValid() && (v.Kind() == reflect.Slice || v.Kind() == reflect.Array) && len(old) > 0 && old[0] == '[' {
		return renderConfigArray(v, old)
	}
	return plain, nil
}

// TOML tree positions and NaN's Go equality rules do not change a value's
// meaning. Ignore them when deciding whether an edit is necessary.
func configValuesEqual(a, b any) bool {
	if tree, ok := a.(*toml.Tree); ok {
		a = tree.ToMap()
	}
	if tree, ok := b.(*toml.Tree); ok {
		b = tree.ToMap()
	}
	if reflect.DeepEqual(a, b) {
		return true
	}
	switch a := a.(type) {
	case float64:
		b, ok := b.(float64)
		return ok && math.IsNaN(a) && math.IsNaN(b)
	case map[string]any:
		b, ok := b.(map[string]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for key, value := range a {
			other, ok := b[key]
			if !ok || !configValuesEqual(value, other) {
				return false
			}
		}
		return true
	case []any:
		b, ok := b.([]any)
		if !ok || len(a) != len(b) {
			return false
		}
		for i := range a {
			if !configValuesEqual(a[i], b[i]) {
				return false
			}
		}
		return true
	}
	return false
}

func renderConfigLiteral(value any) (string, error) {
	switch v := value.(type) {
	case json.Number:
		// SDK settings retain JSON numbers when decoded. Preserve integer
		// precision instead of routing every number through float64.
		if strings.ContainsAny(v.String(), ".eE") {
			n, err := v.Float64()
			if err != nil {
				return "", err
			}
			return renderConfigLiteral(n)
		}
		n, err := v.Int64()
		if err != nil {
			return "", err
		}
		return renderConfigLiteral(n)
	case string:
		return "\"" + string(scanner.Escape(v)) + "\"", nil
	case bool:
		return strconv.FormatBool(v), nil
	case time.Time:
		return v.Format(time.RFC3339Nano), nil
	}
	v := reflect.ValueOf(value)
	if !v.IsValid() {
		return "", fmt.Errorf("cannot write a null TOML value")
	}
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		f := v.Float()
		if math.IsNaN(f) {
			return "nan", nil
		}
		if math.IsInf(f, 1) {
			return "inf", nil
		}
		if math.IsInf(f, -1) {
			return "-inf", nil
		}
		text := strconv.FormatFloat(f, 'g', -1, v.Type().Bits())
		if !strings.ContainsAny(text, ".eE") {
			text += ".0"
		}
		return text, nil
	case reflect.Slice, reflect.Array:
		items := make([]string, v.Len())
		for i := range items {
			var err error
			items[i], err = renderConfigLiteral(v.Index(i).Interface())
			if err != nil {
				return "", err
			}
		}
		return "[" + strings.Join(items, ", ") + "]", nil
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			break
		}
		var keys []string
		for _, key := range v.MapKeys() {
			keys = append(keys, key.String())
		}
		sort.Strings(keys)
		var entries []string
		for _, key := range keys {
			text, err := renderConfigLiteral(v.MapIndex(reflect.ValueOf(key).Convert(v.Type().Key())).Interface())
			if err != nil {
				return "", err
			}
			entries = append(entries, formatConfigPathSegment(key)+" = "+text)
		}
		return "{" + strings.Join(entries, ", ") + "}", nil
	}
	return "", fmt.Errorf("unsupported TOML value %T", value)
}

func renderConfigArray(value reflect.Value, old []byte) (string, error) {
	tokens, err := configTokens(old)
	if err != nil {
		return "", err
	}
	var elements []configToken
	start, depth := -1, 0
	for i := 1; i < len(tokens)-1; i++ {
		token := tokens[i]
		if start < 0 {
			if token.kind == scanner.Comment || token.kind == scanner.Newline || token.kind == scanner.Comma {
				continue
			}
			start = i
		}
		switch token.kind {
		case scanner.LBracket, scanner.LInline:
			depth++
		case scanner.RBracket, scanner.RInline:
			depth--
		}
		if depth == 0 {
			elements = append(elements, configToken{start: tokens[start].start, end: token.end})
			start = -1
		}
	}
	out := old
	if value.Len() > len(elements) {
		out, err = appendConfigArray(value, old, tokens, elements)
		if err != nil {
			return "", err
		}
	}

	for i := len(elements) - 1; i >= 0; i-- {
		element := elements[i]
		if i < value.Len() {
			text, err := renderConfigEdit(value.Index(i).Interface(), old[element.start:element.end])
			if err != nil {
				return "", err
			}
			out = replaceConfigText(out, element.start, element.end, text)
		} else {
			for _, tok := range tokens {
				if tok.start < element.end || tok.kind == scanner.Comment || tok.kind == scanner.Newline {
					continue
				}
				if tok.kind == scanner.Comma {
					// A comment can separate a value from its comma.
					out = replaceConfigText(out, tok.start, tok.end, "")
				}
				break
			}
			out = replaceConfigText(out, element.start, element.end, "")
		}
	}
	return string(out), nil
}

// appendConfigArray retains the existing layout, elements, and comments.
func appendConfigArray(value reflect.Value, old []byte, tokens, elements []configToken) ([]byte, error) {
	out := old
	at := len(old) - 1
	separator, suffix := ", ", ""
	hasComma := false
	if len(elements) > 0 {
		last := elements[len(elements)-1]
		for _, tok := range tokens {
			if tok.start >= last.end && tok.kind == scanner.Comma {
				hasComma = true
			}
		}
	}
	multiline := bytes.ContainsRune(old, '\n')
	if multiline {
		at = configLineStart(old, at)
		if len(bytes.TrimSpace(old[at:len(old)-1])) != 0 {
			at = len(old) - 1
		}
		indent := "  "
		if len(elements) > 0 {
			leading := old[configLineStart(old, elements[0].start):elements[0].start]
			if len(bytes.TrimSpace(leading)) == 0 {
				indent = string(leading)
			}
		}
		separator, suffix = indent, ","+configNewline(old)
	}
	var added strings.Builder
	for i := len(elements); i < value.Len(); i++ {
		text, err := renderConfigLiteral(value.Index(i).Interface())
		if err != nil {
			return nil, err
		}
		if multiline {
			added.WriteString(separator)
		} else if i == len(elements) && hasComma {
			added.WriteString(" ")
		} else if i > 0 {
			added.WriteString(separator)
		}
		added.WriteString(text)
		added.WriteString(suffix)
	}
	if !multiline && hasComma {
		added.WriteString(",")
	}
	out = replaceConfigText(out, at, at, added.String())
	if len(elements) > 0 {
		last := elements[len(elements)-1]
		if multiline && !hasComma {
			out = replaceConfigText(out, last.end, last.end, ",")
		}
	}
	return out, nil
}

func configStringOpeningNewline(old []byte) string {
	if bytes.HasPrefix(old[3:], []byte("\r\n")) {
		return "\r\n"
	}
	if bytes.HasPrefix(old[3:], []byte("\n")) {
		return "\n"
	}
	return ""
}
