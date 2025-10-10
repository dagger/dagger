package dotenv

import (
	"fmt"
	"regexp"
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// GraphEvaluator evaluates dotenv variables with dependency resolution and cycle detection
type GraphEvaluator struct {
	raw          map[string]*parsedEntry // Raw parsed entries (unexpanded)
	expanded     map[string]string       // Memoized expanded values
	expanding    map[string]bool         // Currently expanding (for cycle detection)
	systemLookup func(string) string
}

// parsedEntry stores the parsed but unexpanded shell words
type parsedEntry struct {
	name  string
	value *syntax.Word   // The parsed shell word, not yet expanded
	args  []*syntax.Word // Additional args (for 'foo=a b c' case)
}

// NewGraphEvaluator creates a new graph-based evaluator
func NewGraphEvaluator(environ []string, systemLookup func(string) string) (*GraphEvaluator, error) {
	g := &GraphEvaluator{
		raw:          make(map[string]*parsedEntry),
		expanded:     make(map[string]string),
		expanding:    make(map[string]bool),
		systemLookup: systemLookup,
	}

	// Parse all entries first (without expansion)
	for _, line := range environ {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		entry, err := g.parseEntry(line)
		if err != nil {
			return nil, err
		}
		if entry != nil && entry.name != "" {
			g.raw[entry.name] = entry
		}
	}

	return g, nil
}

// parseEntry parses a line into a parsedEntry without expanding variables
func (g *GraphEvaluator) parseEntry(line string) (*parsedEntry, error) {
	// Try fast path first for simple assignments
	if idx := strings.Index(line, "="); idx != -1 {
		key := line[:idx]
		value := line[idx+1:]

		// Check if this is a simple assignment without shell features
		if simpleKeyRegexp.MatchString(key) && !containsShellFeatures(value) {
			// Create a simple word for the value
			return &parsedEntry{
				name: key,
				value: &syntax.Word{
					Parts: []syntax.WordPart{
						&syntax.Lit{Value: value},
					},
				},
			}, nil
		}
	}

	// Use shell parser for complex cases
	parser := syntax.NewParser()
	file, err := parser.Parse(strings.NewReader(line), "")
	if err != nil {
		return nil, fmt.Errorf("parse error: %q: %w", line, err)
	}
	if len(file.Stmts) == 0 {
		return nil, nil // blank line
	}

	stmt := file.Stmts[0]
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) == 0 {
		return nil, fmt.Errorf("parse error: %q: not a bare assignment", line)
	}

	assigns := call.Assigns
	if len(assigns) != 1 {
		return nil, fmt.Errorf("can't assign multiple variables: %q", line)
	}

	assign := assigns[0]
	if assign.Name == nil {
		return nil, fmt.Errorf("missing name in variable assignment: %q", line)
	}

	return &parsedEntry{
		name:  assign.Name.Value,
		value: assign.Value,
		args:  call.Args,
	}, nil
}

// Lookup gets the expanded value of a variable with memoization
func (g *GraphEvaluator) Lookup(name string) (string, bool, error) {
	// Check if already expanded
	if value, ok := g.expanded[name]; ok {
		return value, true, nil
	}

	// Check if raw entry exists
	entry, ok := g.raw[name]
	if !ok {
		return "", false, nil
	}

	// Check for cycles
	if g.expanding[name] {
		// Build cycle path for better error message
		var cycle []string
		for k := range g.expanding {
			cycle = append(cycle, k)
		}
		return "", false, fmt.Errorf("circular dependency detected: %s -> %s",
			strings.Join(cycle, " -> "), name)
	}

	// Mark as currently expanding
	g.expanding[name] = true
	defer delete(g.expanding, name)

	// Expand the entry
	expanded, err := g.expandEntry(entry)
	if err != nil {
		return "", false, fmt.Errorf("%s: %w", name, err)
	}

	// Memoize the result
	g.expanded[name] = expanded
	return expanded, true, nil
}

// expandEntry expands a parsed entry using the shell expansion logic
func (g *GraphEvaluator) expandEntry(entry *parsedEntry) (string, error) {
	var words []string

	// Handle the value part (if present)
	if entry.value != nil {
		expanded, err := g.expandWord(entry.value)
		if err != nil {
			return "", err
		}
		words = append(words, expanded)
	}

	// Handle additional args (for 'foo=a b c' case)
	for _, arg := range entry.args {
		expanded, err := g.expandWord(arg)
		if err != nil {
			return "", err
		}
		words = append(words, expanded)
	}

	return strings.Join(words, " "), nil
}

// expandWord expands a shell word with variable resolution
func (g *GraphEvaluator) expandWord(w *syntax.Word) (string, error) {
	cfg := &expand.Config{
		Env: expand.FuncEnviron(func(name string) string {
			// First try to expand from our own variables
			if _, ok := g.raw[name]; ok {
				value, _, err := g.Lookup(name)
				if err != nil {
					// On error (e.g., cycle), return empty string
					return ""
				}
				return value
			}

			// Fall back to system lookup
			if g.systemLookup != nil {
				// Don't apply $IFS from the host system
				if name == "IFS" {
					return " \t\n"
				}
				return g.systemLookup(name)
			}

			return ""
		}),
		NoUnset: true,
	}

	// Perform shell-like expansion
	fields, err := expand.Fields(cfg, w)
	if err != nil {
		return "", err
	}
	if len(fields) == 0 {
		return "", nil
	}
	return strings.Join(fields, " "), nil
}

// All returns all variables, expanded
func (g *GraphEvaluator) All() (map[string]string, error) {
	result := make(map[string]string, len(g.raw))

	for name := range g.raw {
		value, _, err := g.Lookup(name)
		if err != nil {
			return nil, err
		}
		result[name] = value
	}

	return result, nil
}

// Evaluate an array of key=value strings in the dotenv syntax,
// and return a map of evaluated variables
func All(environ []string, systemLookup func(string) string) (map[string]string, error) {
	g, err := NewGraphEvaluator(environ, systemLookup)
	if err != nil {
		return nil, err
	}
	return g.All()
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
func Lookup(environ []string, name string, systemLookup func(string) string) (string, bool, error) {
	g, err := NewGraphEvaluator(environ, systemLookup)
	if err != nil {
		return "", false, err
	}
	return g.Lookup(name)
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

// simpleKeyRegexp checks if a key is a valid environment variable name
var simpleKeyRegexp = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

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
