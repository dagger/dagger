// Code generated by dagger. DO NOT EDIT.

package dag

import (
	"context"
	"os"

	dagger "dagger.io/dagger"
)

var client *dagger.Client

func initClient() *dagger.Client {
	if client == nil {
		var err error
		client, err = dagger.Connect(context.Background(), dagger.WithLogOutput(os.Stdout))
		if err != nil {
			panic(err)
		}
	}
	return client
}

// Constructs a cache volume for a given cache key.
func CacheVolume(key string) *dagger.CacheVolume {
	client := initClient()
	return client.CacheVolume(key)
}

// Checks if the current Dagger Engine is compatible with an SDK's required version.
func CheckVersionCompatibility(ctx context.Context, version string) (bool, error) {
	client := initClient()
	return client.CheckVersionCompatibility(ctx, version)
}

// Creates a scratch container or loads one by ID.
//
// Optional platform argument initializes new containers to execute and publish
// as that platform. Platform defaults to that of the builder's host.
func Container(opts ...dagger.ContainerOpts) *dagger.Container {
	client := initClient()
	return client.Container(opts...)
}

// The FunctionCall context that the SDK caller is currently executing in.
// If the caller is not currently executing in a function, this will return
// an error.
func CurrentFunctionCall() *dagger.FunctionCall {
	client := initClient()
	return client.CurrentFunctionCall()
}

// The module currently being served in the session, if any.
func CurrentModule() *dagger.Module {
	client := initClient()
	return client.CurrentModule()
}

// The TypeDef representations of the objects currently being served in the session.
func CurrentTypeDefs(ctx context.Context) ([]dagger.TypeDef, error) {
	client := initClient()
	return client.CurrentTypeDefs(ctx)
}

// The default platform of the builder.
func DefaultPlatform(ctx context.Context) (dagger.Platform, error) {
	client := initClient()
	return client.DefaultPlatform(ctx)
}

// Creates an empty directory or loads one by ID.
func Directory(opts ...dagger.DirectoryOpts) *dagger.Directory {
	client := initClient()
	return client.Directory(opts...)
}

// Loads a file by ID.
//
// Deprecated: Use LoadFileFromID instead.
func File(id dagger.FileID) *dagger.File {
	client := initClient()
	return client.File(id)
}

// Create a function.
func Function(name string, returnType *dagger.TypeDef) *dagger.Function {
	client := initClient()
	return client.Function(name, returnType)
}

// Create a code generation result, given a directory containing the generated
// code.
func GeneratedCode(code *dagger.Directory) *dagger.GeneratedCode {
	client := initClient()
	return client.GeneratedCode(code)
}

// Queries a git repository.
func Git(url string, opts ...dagger.GitOpts) *dagger.GitRepository {
	client := initClient()
	return client.Git(url, opts...)
}

// Queries the host environment.
func Host() *dagger.Host {
	client := initClient()
	return client.Host()
}

// Returns a file containing an http remote url content.
func HTTP(url string, opts ...dagger.HTTPOpts) *dagger.File {
	client := initClient()
	return client.HTTP(url, opts...)
}

// Load a CacheVolume from its ID.
func LoadCacheVolumeFromID(id dagger.CacheVolumeID) *dagger.CacheVolume {
	client := initClient()
	return client.LoadCacheVolumeFromID(id)
}

// Loads a container from an ID.
func LoadContainerFromID(id dagger.ContainerID) *dagger.Container {
	client := initClient()
	return client.LoadContainerFromID(id)
}

// Load a Directory from its ID.
func LoadDirectoryFromID(id dagger.DirectoryID) *dagger.Directory {
	client := initClient()
	return client.LoadDirectoryFromID(id)
}

// Load a File from its ID.
func LoadFileFromID(id dagger.FileID) *dagger.File {
	client := initClient()
	return client.LoadFileFromID(id)
}

// Load a function argument by ID.
func LoadFunctionArgFromID(id dagger.FunctionArgID) *dagger.FunctionArg {
	client := initClient()
	return client.LoadFunctionArgFromID(id)
}

// Load a function by ID.
func LoadFunctionFromID(id dagger.FunctionID) *dagger.Function {
	client := initClient()
	return client.LoadFunctionFromID(id)
}

// Load a GeneratedCode by ID.
func LoadGeneratedCodeFromID(id dagger.GeneratedCodeID) *dagger.GeneratedCode {
	client := initClient()
	return client.LoadGeneratedCodeFromID(id)
}

// Load a git ref from its ID.
func LoadGitRefFromID(id dagger.GitRefID) *dagger.GitRef {
	client := initClient()
	return client.LoadGitRefFromID(id)
}

// Load a git repository from its ID.
func LoadGitRepositoryFromID(id dagger.GitRepositoryID) *dagger.GitRepository {
	client := initClient()
	return client.LoadGitRepositoryFromID(id)
}

// Load a module by ID.
func LoadModuleFromID(id dagger.ModuleID) *dagger.Module {
	client := initClient()
	return client.LoadModuleFromID(id)
}

// Load a Secret from its ID.
func LoadSecretFromID(id dagger.SecretID) *dagger.Secret {
	client := initClient()
	return client.LoadSecretFromID(id)
}

// Loads a service from ID.
func LoadServiceFromID(id dagger.ServiceID) *dagger.Service {
	client := initClient()
	return client.LoadServiceFromID(id)
}

// Load a Socket from its ID.
func LoadSocketFromID(id dagger.SocketID) *dagger.Socket {
	client := initClient()
	return client.LoadSocketFromID(id)
}

// Load a TypeDef by ID.
func LoadTypeDefFromID(id dagger.TypeDefID) *dagger.TypeDef {
	client := initClient()
	return client.LoadTypeDefFromID(id)
}

// Create a new module.
func Module() *dagger.Module {
	client := initClient()
	return client.Module()
}

// Load the static configuration for a module from the given source directory and optional subpath.
func ModuleConfig(sourceDirectory *dagger.Directory, opts ...dagger.ModuleConfigOpts) *dagger.ModuleConfig {
	client := initClient()
	return client.ModuleConfig(sourceDirectory, opts...)
}

// Creates a named sub-pipeline.
func Pipeline(name string, opts ...dagger.PipelineOpts) *dagger.Client {
	client := initClient()
	return client.Pipeline(name, opts...)
}

// Loads a secret from its ID.
//
// Deprecated: Use LoadSecretFromID instead
func Secret(id dagger.SecretID) *dagger.Secret {
	client := initClient()
	return client.Secret(id)
}

// Sets a secret given a user defined name to its plaintext and returns the secret.
// The plaintext value is limited to a size of 128000 bytes.
func SetSecret(name string, plaintext string) *dagger.Secret {
	client := initClient()
	return client.SetSecret(name, plaintext)
}

// Loads a socket by its ID.
//
// Deprecated: Use LoadSocketFromID instead.
func Socket(opts ...dagger.SocketOpts) *dagger.Socket {
	client := initClient()
	return client.Socket(opts...)
}

// Create a new TypeDef.
func TypeDef() *dagger.TypeDef {
	client := initClient()
	return client.TypeDef()
}
