// Code generated by github.com/99designs/gqlgen, DO NOT EDIT.

package main

import (
	"github.com/dagger/cloak/sdk/go/dagger"
)

type Alpine struct {
	Build *dagger.Filesystem `json:"build"`
}

type CacheMountInput struct {
	// Cache mount name
	Name string `json:"name"`
	// Cache mount sharing mode (TODO: switch to enum)
	SharingMode string `json:"sharingMode"`
	// path at which the cache will be mounted
	Path string `json:"path"`
}

// Core API
type Core struct {
	// Fetch an OCI image
	Image *dagger.Filesystem `json:"image"`
	// Fetch a git repository
	Git *dagger.Filesystem `json:"git"`
	// Look up a filesystem by its ID
	Filesystem *dagger.Filesystem `json:"filesystem"`
	// Look up a project by name
	Project *Project `json:"project"`
	// Look up a secret by ID
	Secret string `json:"secret"`
	// Add a secret
	AddSecret dagger.SecretID `json:"addSecret"`
}

type ExecEnvInput struct {
	// Env var name
	Name string `json:"name"`
	// Env var value
	Value string `json:"value"`
}

type ExecInput struct {
	// Command to execute
	// Example: ["echo", "hello, world!"]
	Args []string `json:"args"`
	// Filesystem mounts
	Mounts []*MountInput `json:"mounts"`
	// Cached mounts
	CacheMounts []*CacheMountInput `json:"cacheMounts"`
	// Working directory
	Workdir *string `json:"workdir"`
	// Env vars
	Env []*ExecEnvInput `json:"env"`
	// Secret env vars
	SecretEnv []*ExecSecretEnvInput `json:"secretEnv"`
	// Include the host's ssh agent socket in the exec at the provided path
	SSHAuthSock *string `json:"sshAuthSock"`
}

type ExecSecretEnvInput struct {
	// Env var name
	Name string `json:"name"`
	// Secret env var value
	ID dagger.SecretID `json:"id"`
}

// A schema extension provided by a project
type Extension struct {
	// path to the extension's code within the project's filesystem
	Path string `json:"path"`
	// schema contributed to the project by this extension
	Schema string `json:"schema"`
	// operations contributed to the project by this extension (if any)
	Operations *string `json:"operations"`
	// sdk used to generate code for and/or execute this extension
	Sdk string `json:"sdk"`
}

// Interactions with the user's host filesystem
type Host struct {
	// Fetch the client's workdir
	Workdir *LocalDir `json:"workdir"`
	// Fetch a client directory
	Dir *LocalDir `json:"dir"`
}

// A directory on the user's host filesystem
type LocalDir struct {
	// Read the contents of the directory
	Read *dagger.Filesystem `json:"read"`
	// Write the provided filesystem to the directory
	Write bool `json:"write"`
}

type MountInput struct {
	// filesystem to mount
	Fs dagger.FSID `json:"fs"`
	// path at which the filesystem will be mounted
	Path string `json:"path"`
}

// A set of scripts and/or extensions
type Project struct {
	// name of the project
	Name string `json:"name"`
	// schema provided by the project
	Schema *string `json:"schema"`
	// operations provided by the project
	Operations *string `json:"operations"`
	// extensions in this project
	Extensions []*Extension `json:"extensions"`
	// scripts in this project
	Scripts []*Script `json:"scripts"`
	// other projects with schema this project depends on
	Dependencies []*Project `json:"dependencies"`
	// install the project's schema
	Install bool `json:"install"`
}

// An executable script that uses the project's dependencies and/or extensions
type Script struct {
	// path to the script's code within the project's filesystem
	Path string `json:"path"`
	// sdk used to generate code for and/or execute this script
	Sdk string `json:"sdk"`
}
