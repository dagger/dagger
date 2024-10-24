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
	"runtime"
	"strconv"
	"strings"

	"github.com/containerd/platforms"
	"github.com/dagger/dagger/engine/client"
	"github.com/moby/buildkit/util/gitutil"
	"github.com/spf13/pflag"

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
	case Socket:
		return &socketValue{}
	}
	return nil
}

// GetCustomFlagValueSlice returns a pflag.Value instance for a dagger.ObjectTypeDef name.
func GetCustomFlagValueSlice(name string, defVal []string) (DaggerValue, error) {
	switch name {
	case Container:
		v := &sliceValue[*containerValue]{}
		return v.SetDefault(defVal)
	case Directory:
		v := &sliceValue[*directoryValue]{}
		return v.SetDefault(defVal)
	case File:
		v := &sliceValue[*fileValue]{}
		return v.SetDefault(defVal)
	case Secret:
		v := &sliceValue[*secretValue]{}
		return v.SetDefault(defVal)
	case Service:
		v := &sliceValue[*serviceValue]{}
		return v.SetDefault(defVal)
	case PortForward:
		v := &sliceValue[*portForwardValue]{}
		return v.SetDefault(defVal)
	case CacheVolume:
		v := &sliceValue[*cacheVolumeValue]{}
		return v.SetDefault(defVal)
	case ModuleSource:
		v := &sliceValue[*moduleSourceValue]{}
		return v.SetDefault(defVal)
	case Module:
		v := &sliceValue[*moduleValue]{}
		return v.SetDefault(defVal)
	case Platform:
		v := &sliceValue[*platformValue]{}
		return v.SetDefault(defVal)
	case Socket:
		v := &sliceValue[*socketValue]{}
		return v.SetDefault(defVal)
	}
	return nil, nil
}

// DaggerValue is a pflag.Value that requires a dagger.Client for producing the
// final value.
type DaggerValue interface {
	pflag.Value

	// Get returns the final value for the query builder.
	Get(context.Context, *dagger.Client, *dagger.ModuleSource, *modFunctionArg) (any, error)
}

// sliceValue is a pflag.Value that builds a slice of DaggerValue instances.
//
// NOTE: the code defining this type is heavily inspired by stringSliceValue.Set
// for equivalent behaviour as the other builtin slice types
type sliceValue[T DaggerValue] struct {
	value   []T
	changed bool
	Init    func() T
}

func (v *sliceValue[T]) Type() string {
	var t T
	if v.Init != nil {
		t = v.Init()
	}
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

func (v *sliceValue[T]) Get(ctx context.Context, c *dagger.Client, modSrc *dagger.ModuleSource, modArg *modFunctionArg) (any, error) {
	out := make([]any, len(v.value))
	for i, v := range v.value {
		outV, err := v.Get(ctx, c, modSrc, modArg)
		if err != nil {
			return nil, err
		}
		out[i] = outV
	}
	return out, nil
}

func (v *sliceValue[T]) SetDefault(s []string) (*sliceValue[T], error) {
	if s == nil {
		return v, nil
	}
	if err := v.Set(strings.Join(s, ",")); err != nil {
		return v, err
	}
	v.changed = false
	return v, nil
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
		var vv T

		if v.Init != nil {
			vv = v.Init()
		} else {
			if typ := reflect.TypeOf(vv); typ.Kind() == reflect.Ptr {
				// hack to get a pointer to a new instance of the underlying type
				vv = reflect.New(typ.Elem()).Interface().(T)
			}
		}

		if err := vv.Set(strings.TrimSpace(s)); err != nil {
			return err
		}
		out = append(out, vv)
	}

	if !v.changed {
		v.value = out
	} else {
		v.value = append(v.value, out...)
	}

	v.changed = true
	return nil
}

func newEnumSliceValue(typedef *modEnum, defaultValues []string) *sliceValue[*enumValue] {
	v := &sliceValue[*enumValue]{
		Init: func() *enumValue {
			return newEnumValue(typedef, "")
		},
	}
	for _, defaultValue := range defaultValues {
		v.value = append(v.value, newEnumValue(typedef, defaultValue))
	}
	return v
}

func newEnumValue(typedef *modEnum, defaultValue string) *enumValue {
	v := &enumValue{typedef: typedef}
	v.value = defaultValue
	return v
}

type enumValue struct {
	value   string
	typedef *modEnum
}

var _ DaggerValue = &enumValue{}

func (v *enumValue) Type() string {
	vs := make([]string, 0, len(v.typedef.Values))
	for _, v := range v.typedef.Values {
		vs = append(vs, v.Name)
	}
	return strings.Join(vs, ",")
}

func (v *enumValue) String() string {
	return v.value
}

func (v *enumValue) Get(ctx context.Context, dag *dagger.Client, modSrc *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
	return v.value, nil
}

func (v *enumValue) Set(s string) error {
	for _, allow := range v.typedef.Values {
		if strings.EqualFold(s, allow.Name) {
			v.value = allow.Name
			return nil
		}
	}

	return fmt.Errorf("value should be one of %s", v.Type())
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

func (v *containerValue) Get(_ context.Context, c *dagger.Client, _ *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
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

func (v *directoryValue) Get(ctx context.Context, dag *dagger.Client, modSrc *dagger.ModuleSource, modArg *modFunctionArg) (any, error) {
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
		git := dag.Git(parsedGit.Remote, gitOpts)
		var gitRef *dagger.GitRef
		if parsedGit.Fragment.Ref == "" {
			gitRef = git.Head()
		} else {
			gitRef = git.Branch(parsedGit.Fragment.Ref)
		}
		gitDir := gitRef.Tree()
		if subdir := parsedGit.Fragment.Subdir; subdir != "" {
			gitDir = gitDir.Directory(subdir)
		}
		return gitDir, nil
	}

	// Otherwise it's a local dir path. Allow `file://` scheme or no scheme.
	path := v.String()
	path = strings.TrimPrefix(path, "file://")

	// The core module doesn't have a ModuleSource.
	if modSrc == nil {
		return dag.Host().Directory(path), nil
	}

	// Check if there's a :view.
	// This technically prevents use of paths containing a ":", but that's
	// generally considered a no-no anyways since it isn't in the
	// POSIX "portable filename character set":
	// https://pubs.opengroup.org/onlinepubs/9699919799/basedefs/V1_chap03.html#tag_03_282
	path, viewName, _ := strings.Cut(path, ":")
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	path, err = client.ExpandHomeDir(homeDir, path)
	if err != nil {
		return nil, fmt.Errorf("failed to expand home directory: %w", err)
	}
	path = filepath.ToSlash(path) // make windows paths usable in the Linux engine container

	return modSrc.ResolveDirectoryFromCaller(path, dagger.ModuleSourceResolveDirectoryFromCallerOpts{
		ViewName: viewName,
		Ignore:   modArg.Ignore,
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

func (v *fileValue) Get(_ context.Context, dag *dagger.Client, _ *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
	vStr := v.String()
	if vStr == "" {
		return nil, fmt.Errorf("file path cannot be empty")
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
		git := dag.Git(parsedGit.Remote, gitOpts)
		var gitRef *dagger.GitRef
		if parsedGit.Fragment.Ref == "" {
			gitRef = git.Head()
		} else {
			gitRef = git.Branch(parsedGit.Fragment.Ref)
		}
		gitDir := gitRef.Tree()
		path := parsedGit.Fragment.Subdir
		if path == "" {
			return nil, fmt.Errorf("expected path selection for git repo")
		}
		return gitDir.File(path), nil
	}

	// Otherwise it's a local dir path. Allow `file://` scheme or no scheme.
	vStr = strings.TrimPrefix(vStr, "file://")
	if !filepath.IsAbs(vStr) {
		var err error
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		vStr, err = client.ExpandHomeDir(homeDir, vStr)
		if err != nil {
			return nil, fmt.Errorf("failed to expand home directory: %w", err)
		}
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

func (v *secretValue) Get(ctx context.Context, c *dagger.Client, _ *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
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
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		sourceVal, err := client.ExpandHomeDir(homeDir, v.sourceVal)
		if err != nil {
			return nil, err
		}
		filePlaintext, err := os.ReadFile(sourceVal)
		if err != nil {
			return nil, fmt.Errorf("failed to read secret file %q: %w", v.sourceVal, err)
		}
		plaintext = string(filePlaintext)

	case commandSecretSource:
		var stdoutBytes []byte
		var err error
		if runtime.GOOS == "windows" {
			stdoutBytes, err = exec.CommandContext(ctx, "cmd.exe", "/C", v.sourceVal).Output()
		} else {
			// #nosec G204
			stdoutBytes, err = exec.CommandContext(ctx, "sh", "-c", v.sourceVal).Output()
		}
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
			Protocol: dagger.NetworkProtocolTcp,
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
			Protocol: dagger.NetworkProtocolUdp,
		})
	default:
		return fmt.Errorf("unsupported service address. Must be a valid tcp:// or udp:// URL")
	}
	v.address = s
	return nil
}

func (v *serviceValue) Get(ctx context.Context, c *dagger.Client, _ *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
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

func (v *portForwardValue) Get(_ context.Context, c *dagger.Client, _ *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
	return &dagger.PortForward{
		Frontend: v.frontend,
		Backend:  v.backend,
	}, nil
}

type socketValue struct {
	path string
}

func (v *socketValue) Type() string {
	return Socket
}

func (v *socketValue) String() string {
	return v.path
}

func (v *socketValue) Set(s string) error {
	if s == "" {
		return fmt.Errorf("socket path cannot be empty")
	}
	s = strings.TrimPrefix(s, "unix://") // allow unix:// scheme
	v.path = s
	return nil
}

func (v *socketValue) Get(ctx context.Context, c *dagger.Client, _ *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
	return c.Host().UnixSocket(v.path), nil
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

func (v *cacheVolumeValue) Get(_ context.Context, dag *dagger.Client, _ *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
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

func (v *moduleValue) Get(ctx context.Context, dag *dagger.Client, _ *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
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

func (v *moduleSourceValue) Get(ctx context.Context, dag *dagger.Client, _ *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
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

func (v *platformValue) Get(ctx context.Context, dag *dagger.Client, _ *dagger.ModuleSource, _ *modFunctionArg) (any, error) {
	if v.platform == "" {
		return nil, fmt.Errorf("platform cannot be empty")
	}
	return v.platform, nil
}

// AddFlag adds a flag appropriate for the argument type. Should return a
// pointer to the value.
//
//nolint:gocyclo
func (r *modFunctionArg) AddFlag(flags *pflag.FlagSet) error {
	name := r.FlagName()
	usage := r.Description

	if flags.Lookup(name) != nil {
		return fmt.Errorf("flag already exists: %s", name)
	}

	switch r.TypeDef.Kind {
	case dagger.TypeDefKindStringKind:
		val, _ := getDefaultValue[string](r)
		flags.String(name, val, usage)
		return nil

	case dagger.TypeDefKindIntegerKind:
		val, _ := getDefaultValue[int](r)
		flags.Int(name, val, usage)
		return nil

	case dagger.TypeDefKindBooleanKind:
		val, _ := getDefaultValue[bool](r)
		flags.Bool(name, val, usage)
		return nil

	case dagger.TypeDefKindScalarKind:
		scalarName := r.TypeDef.AsScalar.Name
		defVal, _ := getDefaultValue[string](r)

		if val := GetCustomFlagValue(scalarName); val != nil {
			if defVal != "" {
				val.Set(defVal)
			}
			flags.Var(val, name, usage)
			return nil
		}

		flags.String(name, defVal, usage)
		return nil

	case dagger.TypeDefKindEnumKind:
		enumName := r.TypeDef.AsEnum.Name
		defVal, _ := getDefaultValue[string](r)

		if val := GetCustomFlagValue(enumName); val != nil {
			if defVal != "" {
				val.Set(defVal)
			}
			flags.Var(val, name, usage)
			return nil
		}

		val := newEnumValue(r.TypeDef.AsEnum, defVal)
		flags.Var(val, name, usage)

		return nil

	case dagger.TypeDefKindObjectKind:
		objName := r.TypeDef.AsObject.Name

		if name == "id" && r.TypeDef.AsObject.IsCore() {
			// FIXME: The core TypeDefs have ids converted to objects, but we'd
			// need the CLI to recognize that and either use the object's ID
			// or allow inputing it directly. Just don't support it for now.
			return &UnsupportedFlagError{
				Name: name,
				Type: fmt.Sprintf("%sID", objName),
			}
		}

		if val := GetCustomFlagValue(objName); val != nil {
			flags.Var(val, name, usage)
			return nil
		}

		// TODO: default to JSON?
		return &UnsupportedFlagError{
			Name: name,
			Type: fmt.Sprintf("%q object", objName),
		}

	case dagger.TypeDefKindInputKind:
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

	case dagger.TypeDefKindListKind:
		elementType := r.TypeDef.AsList.ElementTypeDef

		switch elementType.Kind {
		case dagger.TypeDefKindStringKind:
			val, _ := getDefaultValue[[]string](r)
			flags.StringSlice(name, val, usage)
			return nil

		case dagger.TypeDefKindIntegerKind:
			val, _ := getDefaultValue[[]int](r)
			flags.IntSlice(name, val, usage)
			return nil

		case dagger.TypeDefKindBooleanKind:
			val, _ := getDefaultValue[[]bool](r)
			flags.BoolSlice(name, val, usage)
			return nil

		case dagger.TypeDefKindScalarKind:
			scalarName := elementType.AsScalar.Name
			defVal, _ := getDefaultValue[[]string](r)

			val, err := GetCustomFlagValueSlice(scalarName, defVal)
			if err != nil {
				return err
			}
			if val != nil {
				flags.Var(val, name, usage)
				return nil
			}

			flags.StringSlice(name, defVal, usage)
			return nil

		case dagger.TypeDefKindEnumKind:
			enumName := elementType.AsEnum.Name
			defVal, _ := getDefaultValue[[]string](r)

			val, err := GetCustomFlagValueSlice(enumName, defVal)
			if err != nil {
				return err
			}
			if val != nil {
				flags.Var(val, name, usage)
				return nil
			}

			val = newEnumSliceValue(elementType.AsEnum, defVal)
			flags.Var(val, name, usage)

			return nil

		case dagger.TypeDefKindObjectKind:
			objName := elementType.AsObject.Name

			val, err := GetCustomFlagValueSlice(objName, nil)
			if err != nil {
				return err
			}
			if val != nil {
				flags.Var(val, name, usage)
				return nil
			}

			// TODO: default to JSON?
			return &UnsupportedFlagError{
				Name: name,
				Type: fmt.Sprintf("list of %q objects", objName),
			}

		case dagger.TypeDefKindInputKind:
			inputName := elementType.AsInput.Name

			val, err := GetCustomFlagValueSlice(inputName, nil)
			if err != nil {
				return err
			}
			if val != nil {
				flags.Var(val, name, usage)
				return nil
			}

			// TODO: default to JSON?
			return &UnsupportedFlagError{
				Name: name,
				Type: fmt.Sprintf("list of %q inputs", inputName),
			}

		case dagger.TypeDefKindListKind:
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
