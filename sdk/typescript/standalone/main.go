/*
This is a thin wrapper around the TypeScript SDK to workaround https://github.com/dagger/dagger/issues/7583 until the next engine release.
*/
package main

type Ts struct{}

func (m *Ts) ModuleRuntime(modSource *ModuleSource, introspectionJSON string) *Container {
	return modSource.WithSDK("typescript").AsModule().Runtime().WithExec([]string{"npm", "install", "-g", "tsx@4.13.0"}, ContainerWithExecOpts{SkipEntrypoint: true})
}

// Returns lines that match a pattern in the files of the provided Directory
func (m *Ts) Codegen(modSource *ModuleSource, introspectionJSON string) *GeneratedCode {
	return dag.GeneratedCode(modSource.WithSDK("typescript").AsModule().GeneratedContextDirectory())
}

func (m *Ts) RequiredPaths() []string {
	return []string{
		"**/package.json",
		"**/package-lock.json",
		"**/tsconfig.json",
	}
}
