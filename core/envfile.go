package core

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/dagger/dagger/core/dotenv"
	"github.com/dagger/dagger/dagql"
	"github.com/iancoleman/strcase"
	"github.com/vektah/gqlparser/v2/ast"
)

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
	ef = ef.Clone()
	for _, other := range others {
		if other == nil {
			continue
		}
		// Last variable assignment wins: other file's variables win over
		// our own.
		ef.Environ = append(ef.Environ, other.Environ...)
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
func (ef *EnvFile) Variables(ctx context.Context, raw bool) ([]EnvVariable, error) {
	if raw {
		return ef.variablesRaw(ctx)
	}
	return ef.variables(ctx)
}

func (ef *EnvFile) variablesRaw(_ context.Context) (vars []EnvVariable, _ error) {
	for name, value := range dotenv.AllRaw(ef.Environ) {
		vars = append(vars, EnvVariable{Name: name, Value: value})
	}
	return vars, nil
}

func (ef *EnvFile) variables(_ context.Context) (vars []EnvVariable, _ error) {
	hostGetEnv := func(name string) string {
		// FIXME: for now, expanding system env variables is disabled,
		//  until we address the caching issues.
		//  See https://github.com/dagger/dagger/pull/11034#discussion_r2401382370
		// To re-enable, call Host.GetEnv
		return ""
	}
	all, err := dotenv.All(ef.Environ, hostGetEnv)
	if err != nil {
		return nil, err
	}
	for name, value := range all {
		vars = append(vars, EnvVariable{Name: name, Value: value})
	}
	return vars, nil
}

// Filters variables by prefix and removes the prefix from keys.
//
// Variables without the prefix are excluded. For example, with the prefix
// "MY_APP_" and variables:
//
//	MY_APP_TOKEN=topsecret
//	MY_APP_NAME=hello
//	FOO=bar
//
// the resulting environment will contain:
//
//	TOKEN=topsecret
//	NAME=hello
func (ef *EnvFile) Namespace(ctx context.Context, prefix string) (*EnvFile, error) {
	vars, err := ef.Variables(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("Evaluate env file: %w", err)
	}
	result := &EnvFile{
		Expand: ef.Expand,
	}
	for _, variable := range vars {
		if after, match := cutFlexPrefix(variable.Name, prefix); match {
			result = result.WithVariable(after, variable.Value)
		}
	}
	return result, nil
}

// A flexible prefix check, for maximum user convenience
func cutFlexPrefix(s, flexPrefix string) (after string, found bool) {
	for _, toPrefix := range []func(string) string{
		// lower camel case + underscore. Example: "myApp_"
		func(s string) string { return strcase.ToLowerCamel(s) + "_" },
		// snake case + underscore. Example: "my_app_"
		func(s string) string {
			ts := strcase.ToSnake(s)
			if !strings.HasSuffix(ts, "_") {
				ts += "_"
			}
			return ts
		},
	} {
		prefix := toPrefix(flexPrefix)
		if len(s) < len(prefix) {
			continue
		}
		// Case-insensitive match
		if !strings.EqualFold(s[:len(prefix)], prefix) {
			continue
		}
		return s[len(prefix):], true
	}
	return "", false
}

// Return true if the variable exists
func (ef *EnvFile) Exists(name string) bool {
	return dotenv.Exists(ef.Environ, name)
}

// Lookup a variable and return its value, and a 'found' boolean
func (ef *EnvFile) Lookup(ctx context.Context, name string, raw bool) (string, bool, error) {
	if raw {
		value, found := dotenv.LookupRaw(ef.Environ, name)
		return value, found, nil
	}
	hostGetEnv := func(name string) string {
		// FIXME: for now, expanding system env variables is disabled,
		//  until we address the caching issues.
		//  See https://github.com/dagger/dagger/pull/11034#discussion_r2401382370
		// To re-enable, call Host.GetEnv
		return ""
	}
	return dotenv.Lookup(ef.Environ, name, hostGetEnv)
}

func (ef *EnvFile) LookupCaseInsensitive(ctx context.Context, name string) (string, bool, error) {
	all, err := ef.Variables(ctx, false)
	if err != nil {
		return "", false, err
	}
	for _, kv := range all {
		if strings.EqualFold(kv.Name, name) {
			return kv.Value, true, nil
		}
	}
	return "", false, nil
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
