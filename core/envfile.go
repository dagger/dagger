package core

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/dagger/dagger/core/dotenv"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/hashutil"
	"github.com/iancoleman/strcase"
	"github.com/opencontainers/go-digest"
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
	// Context holds variables that are used only for ${...} expansion of the
	// Environ values. They are not exposed via Variables/Lookup/AsFile. This is
	// how Namespace preserves non-matching variables (e.g. a shared ROOT_DIR)
	// that namespaced values still reference but that should not become defaults.
	Context []string `json:"context,omitempty"`
}

var (
	_ dagql.PersistedObject        = (*EnvFile)(nil)
	_ dagql.PersistedObjectDecoder = (*EnvFile)(nil)
)

func (*EnvFile) Type() *ast.Type {
	return &ast.Type{
		NamedType: "EnvFile",
		NonNull:   true,
	}
}

func (*EnvFile) TypeDescription() string {
	return "A collection of environment variables."
}

func (ef *EnvFile) EncodePersistedObject(ctx context.Context, cache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	_ = cache
	if ef == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted env file: nil env file")
	}
	return encodePersistedObjectPayload(ef)
}

func (*EnvFile) DecodePersistedObject(ctx context.Context, dag *dagql.Server, _ uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	_ = ctx
	_ = dag
	var ef EnvFile
	if err := json.Unmarshal(payload, &ef); err != nil {
		return nil, fmt.Errorf("decode persisted env file payload: %w", err)
	}
	return &ef, nil
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

// WithEnvFiles adds multiple environment variables to the EnvFile
func (ef *EnvFile) WithEnvFiles(others ...*EnvFile) *EnvFile {
	ef = ef.Clone()
	for _, other := range others {
		if other == nil {
			continue
		}
		// Last variable assignment wins: other file's variables win over
		// our own.
		ef.Environ = append(ef.Environ, other.Environ...)
		// Carry over hidden expansion context so namespaced values keep
		// resolving their references after files are merged.
		ef.Context = append(ef.Context, other.Context...)
	}
	return ef
}

// Clone creates a deep copy of the EnvFile
func (ef *EnvFile) Clone() *EnvFile {
	cp := *ef
	cp.Environ = slices.Clone(ef.Environ)
	cp.Context = slices.Clone(ef.Context)
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
	return ef.variables(ctx, false)
}

func (ef *EnvFile) variablesRaw(_ context.Context) (vars EnvVariables, _ error) { //nolint:unparam
	for name, value := range dotenv.AllRaw(ef.Environ) {
		vars = append(vars, EnvVariable{Name: name, Value: value})
	}
	vars.Sort() // dotenv.AllRaw returns a map, so we must keep the ordering consistent for hashing purposes
	return vars, nil
}

// allowUnboundVariables should only be used when computing a digest (which is needed to declare values in any order)
func (ef *EnvFile) variables(ctx context.Context, allowUnboundVariables bool) (vars EnvVariables, _ error) {
	hostGetEnv := func(name string) string {
		// Fallback to using host values for expansion
		return Host{}.GetEnv(ctx, name)
	}
	all, err := dotenv.AllWithContext(ef.Environ, ef.Context, hostGetEnv, !allowUnboundVariables)
	if err != nil {
		return nil, err
	}
	for name, value := range all {
		vars = append(vars, EnvVariable{Name: name, Value: value})
	}
	vars.Sort() // dotenv.All returns a map, so we must keep the ordering consistent for hashing purposes
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
	// use raw variables here given that they'll get expanded later when used
	vars, err := ef.Variables(ctx, true)
	if err != nil {
		return nil, fmt.Errorf("Evaluate env file: %w", err)
	}
	result := &EnvFile{
		Expand: ef.Expand,
		// Preserve any hidden expansion context already carried by the source.
		Context: slices.Clone(ef.Context),
	}
	for _, variable := range vars {
		if after, match := cutFlexPrefix(variable.Name, prefix); match {
			result.add(after, variable.Value)
		} else {
			// Keep non-matching variables as hidden expansion context so that
			// namespaced values referencing them (e.g. SOURCE=${ROOT_DIR}) can
			// still be expanded later. They are not exposed as defaults.
			result.Context = append(result.Context, variable.Name+"="+variable.Value)
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
		// Fallback to using host values for expansion
		return Host{}.GetEnv(ctx, name)
	}
	return dotenv.LookupWithContext(ef.Environ, ef.Context, name, hostGetEnv)
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

// AsFile converts the EnvFile to a File containing the environment variables.
func (ef *EnvFile) AsFile(ctx context.Context) (dagql.ObjectResult[*File], error) {
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
		return dagql.ObjectResult[*File]{}, err
	}
	var file dagql.ObjectResult[*File]
	err = srv.Select(ctx, srv.Root(), &file, q...)
	if err != nil {
		return dagql.ObjectResult[*File]{}, err
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
		// Tolerate the common `export KEY=value` convention by stripping the
		// leading `export` keyword before parsing.
		line = dotenv.StripExportPrefix(strings.TrimSpace(line))
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

func (ef *EnvFile) Digest(ctx context.Context) (digest.Digest, error) {
	vars, err := ef.variables(ctx, true)
	if err != nil {
		return "", err
	}
	vals := []string{}
	for _, v := range vars {
		vals = append(vals, fmt.Sprintf("%s=%s", v.Name, v.Value))
	}
	return hashutil.HashStrings(vals...), nil
}
