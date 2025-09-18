package core

import (
	"context"
	"slices"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/vektah/gqlparser/v2/ast"
	"mvdan.cc/sh/v3/shell"
)

type EnvVariable struct {
	Name  string `field:"true" doc:"The environment variable name."`
	Value string `field:"true" doc:"The environment variable value."`
}

func (EnvVariable) Type() *ast.Type {
	return &ast.Type{
		NamedType: "EnvVariable",
		NonNull:   true,
	}
}

func (EnvVariable) TypeDescription() string {
	return "An environment variable name and value."
}

func (EnvVariable) Description() string {
	return "A simple key value object that represents an environment variable."
}

func NewEnvFile(expand bool) *EnvFile {
	return &EnvFile{
		Expand: expand,
	}
}

// EnvFile represents a collection of environment variables that can be manipulated
type EnvFile struct {
	// Variables stored as key-value pairs, preserving order and allowing duplicates
	Environ []string `json:"variables"`
	Expand  bool     `json:"expand"`
}

func (*EnvFile) Type() *ast.Type {
	return &ast.Type{
		NamedType: "EnvFile",
		NonNull:   true,
	}
}

func (*EnvFile) TypeDescription() string {
	return "A collection of environment variables."
}

// Len returns the number of variables in the EnvFile
func (ef *EnvFile) Len() int {
	return len(ef.Environ)
}

// WithVariable adds a new environment variable to the EnvFile
func (ef *EnvFile) WithVariable(name, value string) *EnvFile {
	ef = ef.Clone()
	ef.add(name, value)
	return ef
}

func (ef *EnvFile) WithVariables(variables []EnvVariable) *EnvFile {
	ef = ef.Clone()
	for _, v := range variables {
		ef.add(v.Name, v.Value)
	}
	return ef
}

// WithVariables adds multiple environment variables to the EnvFile
func (ef *EnvFile) WithEnvFiles(others ...*EnvFile) *EnvFile {
	for _, other := range others {
		if other == nil {
			continue
		}
		ef = ef.WithVariables(other.Variables())
	}
	return ef
}

// Clone creates a deep copy of the EnvFile
func (ef *EnvFile) Clone() *EnvFile {
	cp := *ef
	cp.Environ = slices.Clone(ef.Environ)
	return &cp
}

// WithoutVariable removes all occurrences of the named variable
func (ef *EnvFile) WithoutVariable(name string) *EnvFile {
	ef = ef.Clone()
	old := ef.Environ
	ef.Environ = nil
	for _, kv := range old {
		k, _, _ := strings.Cut(kv, "=")
		if k == name {
			continue
		}
		ef.Environ = append(ef.Environ, kv)
	}
	return ef
}

// Variables returns all variables
func (ef *EnvFile) Variables() []EnvVariable {
	vars := make([]EnvVariable, 0, len(ef.Environ))
	expansionLookup := map[string]string{}
	for _, kv := range ef.Environ {
		k, v, _ := strings.Cut(kv, "=")
		if ef.Expand {
			expanded, err := shell.Expand(v, func(k string) string {
				if v, ok := expansionLookup[k]; ok {
					return v
				}
				return ""
			})
			if err == nil {
				v = expanded
			}
			expansionLookup[k] = v
		}
		vars = append(vars, EnvVariable{
			Name:  k,
			Value: v,
		})
	}
	return vars
}

// Search an envfile for variables matching the given module name prefix,
// and return matching variables as a new envfile, with prefix removed
// Example:
//
//	envfile: `
//	  MY_MODULE_TOKEN=topsecret
//	  UNRELATED_SOURCE=.
//	  MY_MODULE_DEBUG=true
//	`
//	modName: "my-module"
//
//	result: `
//	  TOKEN=topsecret
//	  DEBUG=true
//	`
//
// Note: case-insensitive search will be needed to match the resulting variables
// against variable names
func (ef *EnvFile) LookupPrefix(prefix string) *EnvFile {
	result := &EnvFile{
		Expand: ef.Expand,
	}
	for _, variable := range ef.Variables() {
		// eg. "my-module"
		modPrefix := strings.ReplaceAll(prefix, "-", "_") + "_"

		// Does "my_module_token" start with "my_module_"? (case-insensitive)
		if len(variable.Name) < len(modPrefix) || !strings.EqualFold(variable.Name[:len(modPrefix)], modPrefix) {
			continue
		}
		result = result.WithVariable(variable.Name[len(modPrefix):], variable.Value)
	}
	return result
}

// Return true if the variable exists
func (ef *EnvFile) Exists(name string) bool {
	_, found := ef.lookup(name, true)
	return found
}

// Lookup a variable and return its value, and a 'found' boolean
func (ef *EnvFile) Lookup(name string) (string, bool) {
	if !ef.Expand {
		// Optimization: if no expansion, just return the raw value
		return ef.lookup(name, true)
	}
	// Variables() handles expansion
	variables := ef.Variables()
	for _, variable := range variables {
		if variable.Name == name {
			return variable.Value, true
		}
	}
	return "", false
}

func (ef *EnvFile) LookupCaseInsensitive(name string) (string, bool) {
	if !ef.Expand {
		// Optimization: if no expansion, just return the raw value
		return ef.lookup(name, false)
	}
	// Variables() handles expansion
	variables := ef.Variables()
	for _, variable := range variables {
		if strings.EqualFold(variable.Name, name) {
			return variable.Value, true
		}
	}
	return "", false
}

func (ef *EnvFile) lookup(name string, caseSensitive bool) (string, bool) {
	for _, kv := range ef.Environ {
		k, v, _ := strings.Cut(kv, "=")
		if caseSensitive {
			if k == name {
				return v, true
			}
		} else {
			if strings.EqualFold(k, name) {
				return v, true
			}
		}
	}
	return "", false
}

func (ef *EnvFile) add(name, value string) {
	gotOne := false
	for i, v := range ef.Environ {
		k, _, _ := strings.Cut(v, "=")
		if k == name {
			ef.Environ[i] = name + "=" + value
			gotOne = true
			break
		}
	}
	if !gotOne {
		ef.Environ = append(ef.Environ, name+"="+value)
	}
}

// AsFile converts the EnvFile to a File containing the environment variables
func (ef *EnvFile) AsFile(ctx context.Context) (*File, error) {
	// FIXME: expand
	contents := strings.Join(ef.Environ, "\n")
	if len(ef.Environ) > 0 {
		contents += "\n" // Add final newline
	}
	q := []dagql.Selector{
		{
			Field: "file",
			Args: []dagql.NamedInput{
				{
					Name:  "name",
					Value: dagql.NewString(".env"),
				},
				{
					Name:  "contents",
					Value: dagql.NewString(contents),
				},
			},
		},
	}
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	var file *File
	err = srv.Select(ctx, srv.Root(), &file, q...)
	if err != nil {
		return nil, err
	}
	return file, nil
}

// WithContents parses the given contents using joho/godotenv and appends
// variables via EnvFile.WithVariable. Order/duplicates are not preserved.
func (ef *EnvFile) WithContents(contents string) (*EnvFile, error) {
	lines := strings.Split(contents, "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		k := kv[0]
		var v string
		if len(kv) > 1 {
			v = kv[1]
		}
		ef = ef.WithVariable(k, v)
	}
	return ef, nil
}
