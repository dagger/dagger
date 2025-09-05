package core

import (
	"context"
	"os"
	"strings"

	"github.com/dagger/dagger/dagql"
	"github.com/joho/godotenv"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/vektah/gqlparser/v2/ast"
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

// EnvFile represents a collection of environment variables that can be manipulated
type EnvFile struct {
	// Variables stored as key-value pairs, preserving order and allowing duplicates
	Environ []string `json:"variables"`
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

// WithVariable adds a new environment variable to the EnvFile
func (ef *EnvFile) WithVariable(name, value string, expand bool) *EnvFile {
	if expand {
		value = os.Expand(value, func(k string) string {
			v, _ := LookupEnv(ef.Environ, k)
			return v
		})
	}
	ef = ef.Clone()
	ef.Environ = AddEnv(ef.Environ, name, value)
	return ef
}

// Clone creates a deep copy of the EnvFile
func (ef *EnvFile) Clone() *EnvFile {
	newEnviron := make([]string, len(ef.Environ))
	copy(newEnviron, ef.Environ)
	return &EnvFile{
		Environ: newEnviron,
	}
}

// WithoutVariable removes all occurrences of the named variable
func (ef *EnvFile) WithoutVariable(name string) *EnvFile {
	result := &EnvFile{}
	WalkEnv(ef.Environ, func(k, _, env string) {
		if !shell.EqualEnvKeys(k, name) {
			result.Environ = append(result.Environ, env)
		}
	})
	return result
}

// Variables returns all variables, optionally filtered by name
func (ef *EnvFile) Variables() []EnvVariable {
	vars := make([]EnvVariable, 0, len(ef.Environ))
	WalkEnv(ef.Environ, func(k, v, _ string) {
		vars = append(vars, EnvVariable{Name: k, Value: v})
	})
	return vars
}

func (ef *EnvFile) Variable(name string) (string, bool) {
	return LookupEnv(ef.Environ, name)
}

// AsFile converts the EnvFile to a File containing the environment variables
func (ef *EnvFile) File(ctx context.Context) (*File, error) {
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
	vars, err := godotenv.Unmarshal(contents)
	if err != nil {
		return nil, err
	}
	for k, v := range vars {
		ef = ef.WithVariable(k, v, false)
	}
	return ef, nil
}
