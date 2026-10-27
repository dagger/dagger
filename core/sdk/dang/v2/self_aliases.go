package dangv2

import (
	"github.com/dagger/dagger/core"
	"github.com/iancoleman/strcase"
)

// A module's source uses its original API names even when a caller installs it
// under another name. Alias the imported bindings, keeping their actual schema
// types and GraphQL field names intact for requests to the engine.
func moduleSelfAliases(src *core.ModuleSource, dir string) map[string]string {
	if src == nil || src.ModuleOriginalName == "" || src.ModuleName == "" || src.ModuleName == src.ModuleOriginalName {
		return nil
	}
	aliases := map[string]string{strcase.ToLowerCamel(src.ModuleOriginalName): strcase.ToLowerCamel(src.ModuleName)}
	for _, name := range moduleDeclaredTypeNames(dir, src.ModuleOriginalName) {
		original := core.NamespaceObject(name, src.ModuleOriginalName, src.ModuleOriginalName)
		installed := core.NamespaceObject(name, src.ModuleName, src.ModuleOriginalName)
		aliases[original] = installed
	}
	return aliases
}
