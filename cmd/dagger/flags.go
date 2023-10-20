package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"dagger.io/dagger"
	"github.com/spf13/pflag"
)

// GetCustomFlagValue returns a pflag.Value instance for a dagger.ObjectTypeDef name.
func GetCustomFlagValue(name string) pflag.Value {
	switch name {
	case "Container":
		return &containerValue{}
	case "Directory":
		return &directoryValue{}
	case "File":
		return &fileValue{}
	case "Secret":
		return &secretValue{}
	}
	return nil
}

// DaggerValue is a pflag.Value that requires a dagger.Client for producing the final value.
type DaggerValue interface {
	// Get returns the final value for the query builder.
	Get(*dagger.Client) any
}

/*
 * Container
 */

type containerValue struct {
	address string
}

func (v *containerValue) Type() string {
	return "Container"
}

func (v *containerValue) Set(s string) error {
	if s == "" {
		return fmt.Errorf("container address cannot be empty")
	}
	v.address = s
	return nil
}

func (v *containerValue) String() string {
	return v.address
}

func (v *containerValue) Get(c *dagger.Client) *dagger.Container {
	// Default value: flag not set
	if v.String() == "" {
		return nil
	}
	return c.Container().From(v.String())
}

/*
 * Directory
 */

type directoryValue struct {
	path string
}

func (v *directoryValue) Type() string {
	return "Directory"
}

func (v *directoryValue) Set(s string) error {
	if s == "" {
		return fmt.Errorf("directory path cannot be empty")
	}
	v.path = s
	return nil
}

func (v *directoryValue) String() string {
	return v.path
}

func (v *directoryValue) Get(c *dagger.Client) any {
	if v.String() == "" {
		return nil
	}
	return c.Host().Directory(v.String())
}

/*
 * File
 */

// fileValue is a pflag.Value that builds a dagger.File from a host path.
type fileValue struct {
	path string
}

func (v *fileValue) Type() string {
	return "File"
}

func (v *fileValue) Set(s string) error {
	if s != "" {
		return fmt.Errorf("file path cannot be empty")
	}
	v.path = s
	return nil
}

func (v *fileValue) String() string {
	return v.path
}

func (v *fileValue) Get(c *dagger.Client) any {
	if v.String() == "" {
		return nil
	}
	return c.Host().File(v.String())
}

/*
 * Secret
 */

type secretValue struct {
	name      string
	plaintext string
}

func (v *secretValue) Type() string {
	return "Secret"
}

func (v *secretValue) Set(s string) error {
	// NB: If we allow getting the name from the dagger.Secret instance,
	// it can be vulnerable to brute force attacks.
	hash := sha256.Sum256([]byte(s))
	v.name = hex.EncodeToString(hash[:])
	v.plaintext = s
	return nil
}

func (v *secretValue) String() string {
	return v.name
}

func (v *secretValue) Get(c *dagger.Client) any {
	return c.SetSecret(v.name, v.plaintext)
}

/*
 * Built-ins.
 */

// AddFlag adds a flag appropriate for the argument type. Should return a pointer to the value.
func (r *modFunctionArg) AddFlag(flags *pflag.FlagSet, dag *dagger.Client) (any, error) {
	n := r.FlagName()
	d := r.Description

	// TODO: Handle default values.
	// The Go SDK doesn't support them, but Python does.

	switch r.TypeDef.Kind {
	case dagger.Stringkind:
		return flags.String(n, "", d), nil

	case dagger.Integerkind:
		return flags.Int(n, 0, d), nil

	case dagger.Booleankind:
		return flags.Bool(n, false, d), nil

	case dagger.Objectkind:
		objName := r.TypeDef.AsObject.Name

		if val := GetCustomFlagValue(objName); val != nil {
			flags.Var(val, n, d)
			return val, nil
		}

		// TODO: default to JSON?
		return nil, fmt.Errorf("unsupported object type %q", objName)

	case dagger.Listkind:
		elementType := r.TypeDef.AsList.ElementTypeDef

		switch elementType.Kind {
		case dagger.Stringkind:
			return flags.StringSlice(n, nil, d), nil

		case dagger.Integerkind:
			return flags.IntSlice(n, nil, d), nil

		case dagger.Booleankind:
			return flags.BoolSlice(n, nil, d), nil

		case dagger.Objectkind:
			return nil, fmt.Errorf("unsupported list of %q objects", elementType.AsObject.Name)

		case dagger.Listkind:
			return nil, fmt.Errorf("unsupported list of lists")
		}
	}

	return nil, fmt.Errorf("unsupported type for argument: %s", r.Name)
}
