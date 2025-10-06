package dotenv

import (
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// Evaluate an array of key=value strings in the dotenv syntax,
// and return a map of evaluated variables
func All(environ []string) (map[string]string, error) {
	vars := make(map[string]string, len(environ))
	for _, line := range environ {
		line = strings.TrimSpace(line)
		if line == "" {
			continue // skip empty lines
		}
		name, value, err := parseEnvLine(line, vars)
		if err != nil {
			return vars, err
		}
		if name != "" {
			vars[name] = value
		}
	}
	return vars, nil
}

// Evaluate an array of key=value strings in the dotenv syntax,
// in raw mode: values are left unprocessed.
func AllRaw(environ []string) map[string]string {
	vars := make(map[string]string, len(environ))
	for _, kv := range environ {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue // skip empty lines
		}
		name, value, _ := strings.Cut(kv, "=")
		vars[name] = value
	}
	return vars
}

// Evaluate an array of key=value strings in the dotenv syntax,
// and return the value of the specified variable
func Lookup(environ []string, name string) (string, bool, error) {
	vars, err := All(environ)
	if err != nil {
		return "", false, err
	}
	value, ok := vars[name]
	return value, ok, nil
}

// Evaluate an array of key=value strings in the dotenv syntax,
// and return the value of the specified variable in raw mode
func LookupRaw(environ []string, name string) (string, bool) {
	vars := AllRaw(environ)
	value, ok := vars[name]
	return value, ok
}

func Exists(environ []string, name string) bool {
	for _, kv := range environ {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue // skip empty lines
		}
		k, _, _ := strings.Cut(kv, "=")
		if k == name {
			return true
		}
	}
	return false
}

// parseEnvLine parses one line of a dotenv file into (key, value).
func parseEnvLine(line string, lookup map[string]string) (string, string, error) {
	// For simple KEY=VALUE assignments without quotes or $ expansion,
	// handle them directly to avoid shell parser issues with special chars like ()
	if idx := strings.Index(line, "="); idx != -1 {
		key := line[:idx]
		value := line[idx+1:]

		// Check if this is a simple assignment without shell features
		if isSimpleKey(key) && !containsShellFeatures(value) {
			return key, value, nil
		}
	}

	parser := syntax.NewParser()
	file, err := parser.Parse(strings.NewReader(line), "")
	if err != nil {
		return "", "", fmt.Errorf("parse error: %q: %w", line, err)
	}
	if len(file.Stmts) == 0 {
		return "", "", nil // blank line
	}
	stmt := file.Stmts[0]
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) == 0 {
		return "", "", fmt.Errorf("parse error: %q: not a bare assignment", line)
	}
	assigns := call.Assigns
	if len(assigns) != 1 {
		return "", "", fmt.Errorf("can't assign multiple variables: %q", line)
	}
	assign := assigns[0]
	if assign.Name == nil {
		return "", "", fmt.Errorf("missing name in variable assignment: %q", line)
	}
	key := assign.Name.Value

	// dotenv twist: handle 'foo=a b c'
	if assign.Value == nil && len(call.Args) == 0 {
		return key, "", nil // empty assignment
	}
	var words []string
	// Catch the 'foo=a' part of 'foo=a b c'
	if assign.Value != nil {
		assignExpanded, err := expandShellWord(assign.Value, lookup)
		if err != nil {
			return key, "", fmt.Errorf("%s: shell parse error: %w", key, err)
		}
		words = append(words, assignExpanded)
	}
	// Catch the 'b c' part of 'foo=a b c'
	// The shell parser sees those as command arguments
	for _, arg := range call.Args {
		argExpanded, err := expandShellWord(arg, lookup)
		if err != nil {
			return key, "", err
		}
		words = append(words, argExpanded)
	}
	return key, strings.Join(words, " "), nil
}

// isSimpleKey checks if a key is a valid environment variable name
func isSimpleKey(key string) bool {
	if key == "" {
		return false
	}
	for i, ch := range key {
		if !((ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || ch == '_' || (i > 0 && ch >= '0' && ch <= '9')) {
			return false
		}
	}
	return true
}

// containsShellFeatures checks if a value contains shell features that require parsing
func containsShellFeatures(value string) bool {
	for i, ch := range value {
		switch ch {
		// Quotes require shell parser
		case '`', '"', '\'':
			return true
		// Escaping requires shell parser
		case '\\':
			return true
		// Variable expansion requires shell parser
		case '$':
			if i+1 < len(value) {
				next := value[i+1]
				// Check for ${VAR}, $VAR, or $(command)
				if (next >= 'A' && next <= 'Z') ||
					(next >= 'a' && next <= 'z') ||
					next == '_' || next == '{' || next == '(' {
					return true
				}
			}
		}
	}
	return false
}

// expandWord flattens a *syntax.Word into a string.
func expandShellWord(w *syntax.Word, lookup map[string]string) (string, error) {
	cfg := &expand.Config{
		Env: expand.FuncEnviron(func(name string) string {
			value, ok := lookup[name]
			if !ok {
				return ""
			}
			return value
		}),
		NoUnset: true,
	}
	// Perform shell-like expansion: escapes, quotes, parameters
	fields, err := expand.Fields(cfg, w)
	if err != nil {
		return "", err
	}
	if len(fields) == 0 {
		return "", fmt.Errorf("empty expansion")
	}
	return strings.Join(fields, " "), nil
}
