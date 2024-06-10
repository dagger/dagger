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
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/containerd/containerd/platforms"
	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/engine/vcs"
	"github.com/moby/buildkit/util/gitutil"
	"github.com/spf13/pflag"
	"github.com/tonistiigi/fsutil/types"

	"dagger.io/dagger"
)

type UnsupportedFlagError struct {
	Name string
	Type string
}

func (e *UnsupportedFlagError) Error() string {
	msg := fmt.Sprintf("unsupported type for flag --%s", e.Name)
	if e.Type != "" {
		msg = fmt.Sprintf("%s: %s", msg, e.Type)
	}
	return msg
}

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
	case PortForward:
		return &portForwardValue{}
	case CacheVolume:
		return &cacheVolumeValue{}
	case ModuleSource:
		return &moduleSourceValue{}
	case Module:
		return &moduleValue{}
	case Platform:
		return &platformValue{}
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
	case PortForward:
		return &sliceValue[*portForwardValue]{}
	case CacheVolume:
		return &sliceValue[*cacheVolumeValue]{}
	case ModuleSource:
		return &sliceValue[*moduleSourceValue]{}
	case Module:
		return &sliceValue[*moduleValue]{}
	case Platform:
		return &sliceValue[*platformValue]{}
	}
	return nil
}

// DaggerValue is a pflag.Value that requires a dagger.Client for producing the
// final value.
type DaggerValue interface {
	pflag.Value

	// Get returns the final value for the query builder.
	Get(context.Context, *dagger.Client, *dagger.ModuleSource) (any, error)
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

func (v *sliceValue[T]) Get(ctx context.Context, c *dagger.Client, modSrc *dagger.ModuleSource) (any, error) {
	out := make([]any, len(v.value))
	for i, v := range v.value {
		outV, err := v.Get(ctx, c, modSrc)
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

func (v *containerValue) Get(_ context.Context, c *dagger.Client, _ *dagger.ModuleSource) (any, error) {
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

func (v *directoryValue) Get(ctx context.Context, dag *dagger.Client, modSrc *dagger.ModuleSource) (any, error) {
	if v.String() == "" {
		return nil, fmt.Errorf("directory address cannot be empty")
	}

	bk := &osBuildkitClient{}
	ref, kind := vcs.ConvertToBuildKitRef(ctx, v.String(), bk, vcs.ParseRefStringDir)

	if kind == core.ModuleSourceKindGit {
		// Try parsing as a Git URL
		parsedGit, err := parseGit(ref)
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
	}

	// Otherwise it's a local dir path. Allow `file://` scheme or no scheme.
	path := strings.TrimPrefix(ref, "file://")

	// Check if there's a :view.
	// This technically prevents use of paths containing a ":", but that's
	// generally considered a no-no anyways since it isn't in the
	// POSIX "portable filename character set":
	// https://pubs.opengroup.org/onlinepubs/9699919799/basedefs/V1_chap03.html#tag_03_282
	path, viewName, _ := strings.Cut(path, ":")
	path = filepath.ToSlash(path) // make windows paths usable in the Linux engine container
	return modSrc.ResolveDirectoryFromCaller(path, dagger.ModuleSourceResolveDirectoryFromCallerOpts{
		ViewName: viewName,
	}).Sync(ctx)
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

func (v *fileValue) Get(ctx context.Context, dag *dagger.Client, _ *dagger.ModuleSource) (any, error) {
	vStr := v.String()
	if vStr == "" {
		return nil, fmt.Errorf("file path cannot be empty")
	}

	bk := &osBuildkitClient{}
	ref, kind := vcs.ConvertToBuildKitRef(ctx, vStr, bk, vcs.ParseRefStringFile)

	if kind == core.ModuleSourceKindGit {
		// Try parsing as a Git URL
		parsedGit, err := parseGit(ref)
		if err == nil {
			gitOpts := dagger.GitOpts{
				KeepGitDir: true,
			}
			if authSock, ok := os.LookupEnv("SSH_AUTH_SOCK"); ok {
				gitOpts.SSHAuthSocket = dag.Host().UnixSocket(authSock)
			}
			gitDir := dag.Git(parsedGit.Remote, gitOpts).Branch(parsedGit.Fragment.Ref).Tree()
			path := parsedGit.Fragment.Subdir
			if path == "" {
				return nil, fmt.Errorf("expected path selection for git repo")
			}
			return gitDir.File(path), nil
		}

	}
	// Otherwise it's a local dir path. Allow `file://` scheme or no scheme.
	vStr = strings.TrimPrefix(ref, "file://")
	if !filepath.IsAbs(vStr) {
		var err error
		vStr, err = filepath.Abs(vStr)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve absolute path: %w", err)
		}
	}
	vStr = filepath.ToSlash(vStr) // make windows paths usable in the Linux engine container
	return dag.Host().File(vStr), nil
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
	if v.sourceVal == "" {
		return ""
	}
	return fmt.Sprintf("%s:%s", v.secretSource, v.sourceVal)
}

func (v *secretValue) Get(ctx context.Context, c *dagger.Client, _ *dagger.ModuleSource) (any, error) {
	var plaintext string

	switch v.secretSource {
	case envSecretSource:
		envPlaintext, ok := os.LookupEnv(v.sourceVal)
		if !ok {
			// Don't show the entire env var name, in case the user accidentally passed the value instead...
			// This is important because users originally *did* have to pass the value, before we changed to
			// passing by name instead.
			key := v.sourceVal
			if len(key) >= 4 {
				key = key[:3] + "..."
			}
			return nil, fmt.Errorf("secret env var not found: %q", key)
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

func (v *serviceValue) Get(ctx context.Context, c *dagger.Client, _ *dagger.ModuleSource) (any, error) {
	svc, err := c.Host().Service(v.ports, dagger.HostServiceOpts{Host: v.host}).Start(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start service: %w", err)
	}
	return svc, nil
}

// portForwardValue is a pflag.Value that builds a dagger.
type portForwardValue struct {
	frontend int
	backend  int
}

func (v *portForwardValue) Type() string {
	return PortForward
}

func (v *portForwardValue) Set(s string) error {
	if s == "" {
		return fmt.Errorf("portForward setting cannot be empty")
	}

	frontendStr, backendStr, ok := strings.Cut(s, ":")
	if !ok {
		return fmt.Errorf("portForward setting not in the form of frontend:backend: %q", s)
	}

	frontend, err := strconv.Atoi(frontendStr)
	if err != nil {
		return fmt.Errorf("portForward frontend not a valid integer: %q", frontendStr)
	}
	v.frontend = frontend

	backend, err := strconv.Atoi(backendStr)
	if err != nil {
		return fmt.Errorf("portForward backend not a valid integer: %q", backendStr)
	}
	v.backend = backend

	return nil
}

func (v *portForwardValue) String() string {
	return fmt.Sprintf("%d:%d", v.frontend, v.backend)
}

func (v *portForwardValue) Get(_ context.Context, c *dagger.Client, _ *dagger.ModuleSource) (any, error) {
	return &dagger.PortForward{
		Frontend: v.frontend,
		Backend:  v.backend,
	}, nil
}

// cacheVolumeValue is a pflag.Value that builds a dagger.CacheVolume from a
// volume name.
type cacheVolumeValue struct {
	name string
}

func (v *cacheVolumeValue) Type() string {
	return CacheVolume
}

func (v *cacheVolumeValue) Set(s string) error {
	if s == "" {
		return fmt.Errorf("cacheVolume name cannot be empty")
	}
	v.name = s
	return nil
}

func (v *cacheVolumeValue) String() string {
	return v.name
}

func (v *cacheVolumeValue) Get(_ context.Context, dag *dagger.Client, _ *dagger.ModuleSource) (any, error) {
	if v.String() == "" {
		return nil, fmt.Errorf("cacheVolume name cannot be empty")
	}
	return dag.CacheVolume(v.name), nil
}

type moduleValue struct {
	ref string
}

func (v *moduleValue) Type() string {
	return Module
}

func (v *moduleValue) Set(s string) error {
	if s == "" {
		return fmt.Errorf("module ref cannot be empty")
	}
	v.ref = s
	return nil
}

func (v *moduleValue) String() string {
	return v.ref
}

func (v *moduleValue) Get(ctx context.Context, dag *dagger.Client, _ *dagger.ModuleSource) (any, error) {
	if v.ref == "" {
		return nil, fmt.Errorf("module ref cannot be empty")
	}
	modConf, err := getModuleConfigurationForSourceRef(ctx, dag, v.ref, true, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get module configuration: %w", err)
	}
	return modConf.Source.AsModule(), nil
}

type moduleSourceValue struct {
	ref string
}

func (v *moduleSourceValue) Type() string {
	return ModuleSource
}

func (v *moduleSourceValue) Set(s string) error {
	if s == "" {
		return fmt.Errorf("module source ref cannot be empty")
	}
	v.ref = s
	return nil
}

func (v *moduleSourceValue) String() string {
	return v.ref
}

func (v *moduleSourceValue) Get(ctx context.Context, dag *dagger.Client, _ *dagger.ModuleSource) (any, error) {
	if v.ref == "" {
		return nil, fmt.Errorf("module source ref cannot be empty")
	}
	modConf, err := getModuleConfigurationForSourceRef(ctx, dag, v.ref, true, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get module configuration: %w", err)
	}
	return modConf.Source, nil
}

type platformValue struct {
	platform string
}

func (v *platformValue) Type() string {
	return Platform
}

func (v *platformValue) Set(s string) error {
	if s == "" {
		return fmt.Errorf("platform cannot be empty")
	}
	if s == "current" {
		s = platforms.DefaultString()
	}
	v.platform = s
	return nil
}

func (v *platformValue) String() string {
	return v.platform
}

func (v *platformValue) Get(ctx context.Context, dag *dagger.Client, _ *dagger.ModuleSource) (any, error) {
	if v.platform == "" {
		return nil, fmt.Errorf("platform cannot be empty")
	}
	return v.platform, nil
}

// AddFlag adds a flag appropriate for the argument type. Should return a
// pointer to the value.
func (r *modFunctionArg) AddFlag(flags *pflag.FlagSet) error {
	name := r.FlagName()
	usage := r.Description

	if flags.Lookup(name) != nil {
		return fmt.Errorf("flag already exists: %s", name)
	}

	switch r.TypeDef.Kind {
	case dagger.StringKind:
		val, _ := getDefaultValue[string](r)
		flags.String(name, val, usage)
		return nil

	case dagger.IntegerKind:
		val, _ := getDefaultValue[int](r)
		flags.Int(name, val, usage)
		return nil

	case dagger.BooleanKind:
		val, _ := getDefaultValue[bool](r)
		flags.Bool(name, val, usage)
		return nil

	case dagger.ScalarKind:
		scalarName := r.TypeDef.AsScalar.Name

		if val := GetCustomFlagValue(scalarName); val != nil {
			flags.Var(val, name, usage)
			return nil
		}

		val, _ := getDefaultValue[string](r)
		flags.String(name, val, usage)
		return nil

	case dagger.ObjectKind:
		objName := r.TypeDef.AsObject.Name

		if val := GetCustomFlagValue(objName); val != nil {
			flags.Var(val, name, usage)
			return nil
		}

		// TODO: default to JSON?
		return &UnsupportedFlagError{
			Name: name,
			Type: fmt.Sprintf("%q object", objName),
		}

	case dagger.InputKind:
		inputName := r.TypeDef.AsInput.Name

		if val := GetCustomFlagValue(inputName); val != nil {
			flags.Var(val, name, usage)
			return nil
		}

		// TODO: default to JSON?
		return &UnsupportedFlagError{
			Name: name,
			Type: fmt.Sprintf("%q input", inputName),
		}

	case dagger.ListKind:
		elementType := r.TypeDef.AsList.ElementTypeDef

		switch elementType.Kind {
		case dagger.StringKind:
			val, _ := getDefaultValue[[]string](r)
			flags.StringSlice(name, val, usage)
			return nil

		case dagger.IntegerKind:
			val, _ := getDefaultValue[[]int](r)
			flags.IntSlice(name, val, usage)
			return nil

		case dagger.BooleanKind:
			val, _ := getDefaultValue[[]bool](r)
			flags.BoolSlice(name, val, usage)
			return nil

		case dagger.ScalarKind:
			scalarName := elementType.AsScalar.Name

			if val := GetCustomFlagValueSlice(scalarName); val != nil {
				flags.Var(val, name, usage)
				return nil
			}

			val, _ := getDefaultValue[[]string](r)
			flags.StringSlice(name, val, usage)
			return nil

		case dagger.ObjectKind:
			objName := elementType.AsObject.Name

			if val := GetCustomFlagValueSlice(objName); val != nil {
				flags.Var(val, name, usage)
				return nil
			}

			// TODO: default to JSON?
			return &UnsupportedFlagError{
				Name: name,
				Type: fmt.Sprintf("list of %q objects", objName),
			}

		case dagger.InputKind:
			inputName := elementType.AsInput.Name

			if val := GetCustomFlagValueSlice(inputName); val != nil {
				flags.Var(val, name, usage)
				return nil
			}

			// TODO: default to JSON?
			return &UnsupportedFlagError{
				Name: name,
				Type: fmt.Sprintf("list of %q inputs", inputName),
			}

		case dagger.ListKind:
			return &UnsupportedFlagError{
				Name: name,
				Type: "list of lists",
			}
		}
	}

	return &UnsupportedFlagError{Name: name}
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

type osBuildkitClient struct{}

// cliBuildkitClient.StatCallerHostPath is the CLI context implementation of StatCallerHostPath
func (c *osBuildkitClient) StatCallerHostPath(ctx context.Context, path string, followLinks bool) (*types.Stat, error) {
	var fileInfo os.FileInfo
	var err error

	if followLinks {
		fileInfo, err = os.Stat(path)
	} else {
		fileInfo, err = os.Lstat(path)
	}

	if err != nil {
		return nil, err
	}

	return &types.Stat{
		Path: path,
		Mode: uint32(fileInfo.Mode()),
	}, nil
}
