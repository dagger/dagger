package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"reflect"
	"strings"

	"dagger.io/dagger"
	"github.com/spf13/pflag"
)

// GetCustomFlagValue returns a pflag.Value instance for a dagger.ObjectTypeDef name.
func GetCustomFlagValue(name string) pflag.Value {
	switch name {
	case Container:
		return &containerValue{}
	case Directory:
		return &directoryValue{}
	case File:
		return &fileValue{}
	case Secret:
		return &secretValue{}
	}
	return nil
}

// GetCustomFlagValueSlice returns a pflag.Value instance for a dagger.ObjectTypeDef name.
func GetCustomFlagValueSlice(name string) pflag.Value {
	switch name {
	case Container:
		return &sliceValue[*containerValue]{}
	case Directory:
		return &sliceValue[*directoryValue]{}
	case File:
		return &sliceValue[*fileValue]{}
	case Secret:
		return &sliceValue[*secretValue]{}
	}
	return nil
}

// DaggerValue is a pflag.Value that requires a dagger.Client for producing the
// final value.
type DaggerValue interface {
	pflag.Value

	// Get returns the final value for the query builder.
	Get(*dagger.Client) any
}

// sliceValue is a pflag.Value that builds a slice of DaggerValue instances.
//
// NOTE: the code defining this type is heavily inspired by stringSliceValue.Set
// for equivalent behaviour as the other builtin slice types
type sliceValue[T DaggerValue] struct {
	value []T
}

func (v *sliceValue[T]) Type() string {
	var t T
	return t.Type()
}

func (v *sliceValue[T]) String() string {
	ss := []string{}
	for _, v := range v.value {
		ss = append(ss, v.String())
	}
	out, _ := writeAsCSV(ss)
	return "[" + out + "]"
}

func (v *sliceValue[T]) Get(c *dagger.Client) any {
	out := make([]any, len(v.value))
	for i, v := range v.value {
		out[i] = v.Get(c)
	}
	return out
}

func (v *sliceValue[T]) Set(s string) error {
	// remove all quote characters
	rmQuote := strings.NewReplacer(`"`, "", `'`, "", "`", "")

	// read flag arguments with CSV parser
	ss, err := readAsCSV(rmQuote.Replace(s))
	if err != nil && err != io.EOF {
		return err
	}

	// parse values into slice
	out := make([]T, 0, len(ss))
	for _, s := range ss {
		var v T
		if typ := reflect.TypeOf(v); typ.Kind() == reflect.Ptr {
			// hack to get a pointer to a new instance of the underlying type
			v = reflect.New(typ.Elem()).Interface().(T)
		}

		if err := v.Set(strings.TrimSpace(s)); err != nil {
			return err
		}
		out = append(out, v)
	}

	v.value = append(v.value, out...)
	return nil
}

// containerValue is a pflag.Value that builds a dagger.Container from a
// base image name.
type containerValue struct {
	address string
}

func (v *containerValue) Type() string {
	return Container
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

func (v *containerValue) Get(c *dagger.Client) any {
	if v.address == "" {
		return nil
	}
	return c.Container().From(v.String())
}

// directoryValue is a pflag.Value that builds a dagger.Directory from a host path.
type directoryValue struct {
	path string
}

func (v *directoryValue) Type() string {
	return Directory
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

// fileValue is a pflag.Value that builds a dagger.File from a host path.
type fileValue struct {
	path string
}

func (v *fileValue) Type() string {
	return File
}

func (v *fileValue) Set(s string) error {
	if s == "" {
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

// secretValue is a pflag.Value that builds a dagger.Secret from a name and a
// plaintext value.
type secretValue struct {
	name      string
	plaintext string
}

func (v *secretValue) Type() string {
	return Secret
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

// AddFlag adds a flag appropriate for the argument type. Should return a
// pointer to the value.
func (r *modFunctionArg) AddFlag(flags *pflag.FlagSet, dag *dagger.Client) (any, error) {
	name := r.FlagName()
	usage := r.Description

	if flags.Lookup(name) != nil {
		return nil, fmt.Errorf("flag already exists: %s", name)
	}

	switch r.TypeDef.Kind {
	case dagger.Stringkind:
		val, _ := getDefaultValue[string](r)
		return flags.String(name, val, usage), nil

	case dagger.Integerkind:
		val, _ := getDefaultValue[int](r)
		return flags.Int(name, val, usage), nil

	case dagger.Booleankind:
		val, _ := getDefaultValue[bool](r)
		return flags.Bool(name, val, usage), nil

	case dagger.Objectkind:
		objName := r.TypeDef.AsObject.Name

		if val := GetCustomFlagValue(objName); val != nil {
			flags.Var(val, name, usage)
			return val, nil
		}

		// TODO: default to JSON?
		return nil, fmt.Errorf("unsupported object type %q for flag: %s", objName, name)

	case dagger.Listkind:
		elementType := r.TypeDef.AsList.ElementTypeDef

		switch elementType.Kind {
		case dagger.Stringkind:
			val, _ := getDefaultValue[[]string](r)
			return flags.StringSlice(name, val, usage), nil

		case dagger.Integerkind:
			val, _ := getDefaultValue[[]int](r)
			return flags.IntSlice(name, val, usage), nil

		case dagger.Booleankind:
			val, _ := getDefaultValue[[]bool](r)
			return flags.BoolSlice(name, val, usage), nil

		case dagger.Objectkind:
			objName := elementType.AsObject.Name

			if val := GetCustomFlagValueSlice(objName); val != nil {
				flags.Var(val, name, usage)
				return val, nil
			}

			// TODO: default to JSON?
			return nil, fmt.Errorf("unsupported list of objects %q for flag: %s", objName, name)

		case dagger.Listkind:
			return nil, fmt.Errorf("unsupported list of lists for flag: %s", name)
		}
	}

	return nil, fmt.Errorf("unsupported type for argument: %s", r.Name)
}

func readAsCSV(val string) ([]string, error) {
	if val == "" {
		return []string{}, nil
	}
	stringReader := strings.NewReader(val)
	csvReader := csv.NewReader(stringReader)
	return csvReader.Read()
}

func writeAsCSV(vals []string) (string, error) {
	b := &bytes.Buffer{}
	w := csv.NewWriter(b)
	err := w.Write(vals)
	if err != nil {
		return "", err
	}
	w.Flush()
	return strings.TrimSuffix(b.String(), "\n"), nil
}
