package introspection

// renamedCoreNames are the core schema names renamed for the naming rules
// (see hack/designs/identifier-casing.md, "Core schema"), each with its old
// schema name, which the engine still accepts on v1 views as a hidden alias.
// The schema doesn't show the old names, so codegen that keeps deprecated
// aliases of the identifiers it derived from them finds them here.
//
// Keys are "Type.field" for fields and input object fields, and
// "Type.field(arg)" for arguments.
var renamedCoreNames = map[string]string{
	"Artifacts.filterURI":               "filterUri",
	"Artifacts.withoutURI":              "withoutUri",
	"GitCommit.parentSHAs":              "parentShas",
	"GitCommit.shortSHA":                "shortSha",
	"GitRepository.withRemote(pushURL)": "pushUrl",
	"LLM.withToolResult(callID)":        "callId",
	"LLMContentBlock.callID":            "callId",
	"LLMContentBlockInput.callID":       "callId",
	"Workspace.withSDK(asSDKName)":      "asSdkName",
}

// LegacyCoreName returns the schema name a core field or input object field,
// name of typeName, had before it was renamed, or name itself if it wasn't.
func LegacyCoreName(typeName, name string) string {
	if legacy, ok := renamedCoreNames[typeName+"."+name]; ok {
		return legacy
	}
	return name
}

// LegacyCoreArgName returns the schema name an argument of a core field had
// before it was renamed, or arg itself if it wasn't.
func LegacyCoreArgName(typeName, field, arg string) string {
	if legacy, ok := renamedCoreNames[typeName+"."+field+"("+arg+")"]; ok {
		return legacy
	}
	return arg
}

// LegacyName returns the schema name f had before it was renamed, if it's a
// renamed core field, or its name.
func (f Field) LegacyName() string {
	if f.ParentObject == nil {
		return f.Name
	}
	return LegacyCoreName(f.ParentObject.Name, f.Name)
}

// LegacyArgName returns the schema name arg, an argument of f, had before it
// was renamed, if it's a renamed argument of a core field, or its name.
func (f Field) LegacyArgName(arg string) string {
	if f.ParentObject == nil {
		return arg
	}
	return LegacyCoreArgName(f.ParentObject.Name, f.Name, arg)
}
