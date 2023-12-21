package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"

	"dagger.io/dagger"
	"github.com/moby/buildkit/util/gitutil"
	"github.com/spf13/pflag"
)

// GetCustomFlagValue returns a pflag.Value instance for a dagger.ObjectTypeDef name.
func GetCustomFlagValue(name string) DaggerValue {
	switch name {
	case Container:
		return &containerValue{}
	case Directory:
		return &directoryValue{}
	case File:
		return &fileValue{}
	case Secret:
		return &secretValue{}
	case Service:
		return &serviceValue{}
	}
	return nil
}

// GetCustomFlagValueSlice returns a pflag.Value instance for a dagger.ObjectTypeDef name.
func GetCustomFlagValueSlice(name string) DaggerValue {
	switch name {
	case Container:
		return &sliceValue[*containerValue]{}
	case Directory:
		return &sliceValue[*directoryValue]{}
	case File:
		return &sliceValue[*fileValue]{}
	case Secret:
		return &sliceValue[*secretValue]{}
	case Service:
		return &sliceValue[*serviceValue]{}
	}
	return nil
}

// DaggerValue is a pflag.Value that requires a dagger.Client for producing the
// final value.
type DaggerValue interface {
	pflag.Value

	// Get returns the final value for the query builder.
	Get(context.Context, *dagger.Client) (any, error)
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

func (v *sliceValue[T]) Get(ctx context.Context, c *dagger.Client) (any, error) {
	out := make([]any, len(v.value))
	for i, v := range v.value {
		outV, err := v.Get(ctx, c)
		if err != nil {
			return nil, err
		}
		out[i] = outV
	}
	return out, nil
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

func (v *containerValue) Get(_ context.Context, c *dagger.Client) (any, error) {
	if v.address == "" {
		return nil, fmt.Errorf("container address cannot be empty")
	}
	return c.Container().From(v.String()), nil
}

// directoryValue is a pflag.Value that builds a dagger.Directory from a host path.
type directoryValue struct {
	address string
}

func (v *directoryValue) Type() string {
	return Directory
}

func (v *directoryValue) Set(s string) error {
	if s == "" {
		return fmt.Errorf("directory address cannot be empty")
	}
	v.address = s
	return nil
}

func (v *directoryValue) String() string {
	return v.address
}

func parseGit(urlStr string) (*gitutil.GitURL, error) {
	// FIXME: handle tarball-over-http (where http(s):// is scheme but not a git repo)
	u, err := gitutil.ParseURL(urlStr)
	if err != nil {
		return nil, err
	}
	if u.Fragment == nil {
		u.Fragment = &gitutil.GitURLFragment{}
	}
	if u.Fragment.Ref == "" {
		// FIXME: default branch can be remotely looked up, but that would
		// require 1) a context, 2) a way to return an error, 3) more time than I have :)
		u.Fragment.Ref = "main"
	}
	return u, nil
}

func (v *directoryValue) Get(_ context.Context, dag *dagger.Client) (any, error) {
	if v.String() == "" {
		return nil, fmt.Errorf("directory address cannot be empty")
	}

	// Try parsing as a Git URL
	parsedGit, err := parseGit(v.String())
	if err == nil {
		gitOpts := dagger.GitOpts{
			KeepGitDir: true,
		}
		if authSock, ok := os.LookupEnv("SSH_AUTH_SOCK"); ok {
			gitOpts.SSHAuthSocket = dag.Host().UnixSocket(authSock)
		}
		gitDir := dag.Git(parsedGit.Remote, gitOpts).Branch(parsedGit.Fragment.Ref).Tree()
		if subdir := parsedGit.Fragment.Subdir; subdir != "" {
			gitDir = gitDir.Directory(subdir)
		}
		return gitDir, nil
	}

	// Otherwise it's a local dir path. Allow `file://` scheme or no scheme.
	vStr := v.String()
	vStr = strings.TrimPrefix(vStr, "file://")
	return dag.Host().Directory(vStr), nil
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

func (v *fileValue) Get(_ context.Context, c *dagger.Client) (any, error) {
	if v.String() == "" {
		return nil, fmt.Errorf("file path cannot be empty")
	}
	return c.Host().File(v.String()), nil
}

// secretValue is a pflag.Value that builds a dagger.Secret from a name and a
// plaintext value.
type secretValue struct {
	secretSource string
	sourceVal    string
}

const (
	envSecretSource     = "env"
	fileSecretSource    = "file"
	commandSecretSource = "cmd"
)

func (v *secretValue) Type() string {
	return Secret
}

func (v *secretValue) Set(s string) error {
	secretSource, val, ok := strings.Cut(s, ":")
	if !ok {
		// case of e.g. `--token MY_ENV_SECRET`, which is shorthand for `--token env:MY_ENV_SECRET`
		val = secretSource
		secretSource = envSecretSource
	}
	v.secretSource = secretSource
	v.sourceVal = val

	return nil
}

func (v *secretValue) String() string {
	return fmt.Sprintf("%s:%s", v.secretSource, v.sourceVal)
}

func (v *secretValue) Get(ctx context.Context, c *dagger.Client) (any, error) {
	var plaintext string

	switch v.secretSource {
	case envSecretSource:
		envPlaintext, ok := os.LookupEnv(v.sourceVal)
		if !ok {
			return nil, fmt.Errorf("secret env var not found %q", v.sourceVal)
		}
		plaintext = envPlaintext

	case fileSecretSource:
		filePlaintext, err := os.ReadFile(v.sourceVal)
		if err != nil {
			return nil, fmt.Errorf("failed to read secret file %q: %w", v.sourceVal, err)
		}
		plaintext = string(filePlaintext)

	case commandSecretSource:
		// #nosec G204
		stdoutBytes, err := exec.CommandContext(ctx, "sh", "-c", v.sourceVal).Output()
		if err != nil {
			return nil, fmt.Errorf("failed to run secret command %q: %w", v.sourceVal, err)
		}
		plaintext = string(stdoutBytes)

	default:
		return nil, fmt.Errorf("unsupported secret arg source: %q", v.secretSource)
	}

	// NB: If we allow getting the name from the dagger.Secret instance,
	// it can be vulnerable to brute force attacks.
	hash := sha256.Sum256([]byte(plaintext))
	secretName := hex.EncodeToString(hash[:])
	return c.SetSecret(secretName, plaintext), nil
}

// serviceValue is a pflag.Value that builds a dagger.Service from a host:port
// combination.
type serviceValue struct {
	address string // for string representation
	host    string
	ports   []dagger.PortForward
}

func (v *serviceValue) Type() string {
	return Service
}

func (v *serviceValue) String() string {
	return v.address
}

func (v *serviceValue) Set(s string) error {
	if s == "" {
		return fmt.Errorf("service address cannot be empty")
	}
	u, err := url.Parse(s)
	if err != nil {
		return err
	}
	switch u.Scheme {
	case "tcp":
		host, port, err := net.SplitHostPort(u.Host)
		if err != nil {
			return err
		}
		nPort, err := strconv.Atoi(port)
		if err != nil {
			return err
		}
		v.host = host
		v.ports = append(v.ports, dagger.PortForward{
			Backend:  nPort,
			Frontend: nPort,
			Protocol: dagger.Tcp,
		})
	case "udp":
		host, port, err := net.SplitHostPort(u.Host)
		if err != nil {
			return err
		}
		nPort, err := strconv.Atoi(port)
		if err != nil {
			return err
		}
		v.host = host
		v.ports = append(v.ports, dagger.PortForward{
			Backend:  nPort,
			Frontend: nPort,
			Protocol: dagger.Udp,
		})
	default:
		return fmt.Errorf("unsupported service address. Must be a valid tcp:// or udp:// URL")
	}
	v.address = s
	return nil
}

func (v *serviceValue) Get(_ context.Context, c *dagger.Client) (any, error) {
	return c.Host().Service(v.ports, dagger.HostServiceOpts{Host: v.host}), nil
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
