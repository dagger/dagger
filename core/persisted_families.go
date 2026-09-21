package core

import "github.com/dagger/dagger/dagql"

// persistedObjectFamilies is the inventory of every persisted object codec
// declared by this package: the Go payload family written into object
// envelopes, the Go type whose codec produces it, and the pure reference
// visitor that walks its declared references without constructing the value.
// Registration happens at initialization, independently of which schemas a
// session installs. A codec without a family cannot be encoded, so an
// unfinished type fails loudly instead of persisting an unreadable payload.
var persistedObjectFamilies = []dagql.PersistedObjectFamily{
	// Filesystems and resources.
	{Name: "core.Container", Typed: (*Container)(nil), Visitor: persistedContainerVisitor, Transfer: foreignFamilyCodec("Container")},
	{Name: "core.Directory", Typed: (*Directory)(nil), Visitor: persistedDirectoryVisitor, Transfer: foreignFamilyCodec("Directory")},
	{Name: "core.File", Typed: (*File)(nil), Visitor: persistedFileVisitor, Transfer: foreignFamilyCodec("File")},
	{Name: "core.Service", Typed: (*Service)(nil), Visitor: persistedServiceVisitor},
	{Name: "core.Volume", Typed: (*Volume)(nil), Visitor: persistedVolumeVisitor},
	{Name: "core.Secret", Typed: (*Secret)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.Socket", Typed: (*Socket)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.CacheVolume", Typed: (*CacheVolume)(nil), Visitor: persistedCacheVolumeVisitor, Transfer: foreignFamilyCodec("CacheVolume")},
	{Name: "core.ClientFilesyncMirror", Typed: (*ClientFilesyncMirror)(nil), Visitor: persistedClientFilesyncMirrorVisitor, Transfer: foreignFamilyCodec("ClientFilesyncMirror")},
	{Name: "core.RemoteGitMirror", Typed: (*RemoteGitMirror)(nil), Visitor: persistedRemoteGitMirrorVisitor, Transfer: foreignFamilyCodec("RemoteGitMirror")},

	// Module, workspace and action state.
	{Name: "core.Module", Typed: (*Module)(nil), Visitor: persistedModuleVisitor},
	{Name: "core.ModuleSource", Typed: (*ModuleSource)(nil), Visitor: persistedModuleSourceVisitor, Transfer: foreignFamilyCodec("ModuleSource")},
	{Name: "core.ModuleObject", Typed: (*ModuleObject)(nil), Visitor: persistedModuleObjectVisitor},
	{Name: "core.Workspace", Typed: (*Workspace)(nil), Visitor: persistedWorkspaceVisitor},
	{Name: "core.WorkspaceGit", Typed: (*WorkspaceGit)(nil), Visitor: persistedWorkspaceGitVisitor},
	{Name: "core.WorkspaceModule", Typed: (*WorkspaceModule)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.WorkspaceModuleSetting", Typed: (*WorkspaceModuleSetting)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.WorkspaceSDK", Typed: (*WorkspaceSDK)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.Generator", Typed: (*Generator)(nil), Visitor: persistedGeneratorVisitor},
	{Name: "core.GeneratorGroup", Typed: (*GeneratorGroup)(nil), Visitor: persistedGeneratorGroupVisitor},

	// Immutable outputs.
	{Name: "core.GitRepository", Typed: (*GitRepository)(nil), Visitor: persistedGitRepositoryVisitor},
	{Name: "core.GitRef", Typed: (*GitRef)(nil), Visitor: persistedGitRefVisitor},
	{Name: "core.GitCommit", Typed: (*GitCommit)(nil), Visitor: persistedGitCommitVisitor},
	{Name: "core.GitBundle", Typed: (*GitBundle)(nil), Visitor: persistedGitBundleVisitor},
	{Name: "core.HTTPState", Typed: (*HTTPState)(nil), Visitor: persistedHTTPStateVisitor, Transfer: foreignFamilyCodec("HTTPState")},
	{Name: "core.Changeset", Typed: (*Changeset)(nil), Visitor: persistedChangesetVisitor},
	{Name: "core.GeneratedCode", Typed: (*GeneratedCode)(nil), Visitor: persistedGeneratedCodeVisitor},
	{Name: "core.SearchResult", Typed: (*SearchResult)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.SearchSubmatch", Typed: (*SearchSubmatch)(nil), Visitor: dagql.PersistedNoReferences{}},

	// Definitions.
	{Name: "core.Function", Typed: (*Function)(nil), Visitor: persistedFunctionVisitor},
	{Name: "core.FunctionArg", Typed: (*FunctionArg)(nil), Visitor: persistedFunctionArgVisitor},
	{Name: "core.TypeDef", Typed: (*TypeDef)(nil), Visitor: persistedTypeDefVisitor},
	{Name: "core.ObjectTypeDef", Typed: (*ObjectTypeDef)(nil), Visitor: persistedObjectTypeDefVisitor},
	{Name: "core.FieldTypeDef", Typed: (*FieldTypeDef)(nil), Visitor: persistedFieldTypeDefVisitor},
	{Name: "core.InterfaceTypeDef", Typed: (*InterfaceTypeDef)(nil), Visitor: persistedInterfaceTypeDefVisitor},
	{Name: "core.ScalarTypeDef", Typed: (*ScalarTypeDef)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.ListTypeDef", Typed: (*ListTypeDef)(nil), Visitor: persistedListTypeDefVisitor},
	{Name: "core.InputTypeDef", Typed: (*InputTypeDef)(nil), Visitor: persistedInputTypeDefVisitor},
	{Name: "core.EnumTypeDef", Typed: (*EnumTypeDef)(nil), Visitor: persistedEnumTypeDefVisitor},
	{Name: "core.EnumMemberTypeDef", Typed: (*EnumMemberTypeDef)(nil), Visitor: persistedEnumMemberTypeDefVisitor},

	// Data and reflection.
	{Name: "core.Address", Typed: (*Address)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.Host", Typed: (*Host)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.EnvFile", Typed: (*EnvFile)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.JSONValue", Typed: (*JSONValue)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.Error", Typed: (*Error)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.ErrorValue", Typed: (*ErrorValue)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.DiffStat", Typed: (*DiffStat)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.Stat", Typed: (*Stat)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.FunctionCall", Typed: (*FunctionCall)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.FunctionCallArgValue", Typed: (*FunctionCallArgValue)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.SourceMap", Typed: (*SourceMap)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.LLMTokenUsage", Typed: (*LLMTokenUsage)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.LLMVariable", Typed: (*LLMVariable)(nil), Visitor: dagql.PersistedNoReferences{}},
}

func init() {
	for _, family := range persistedObjectFamilies {
		dagql.RegisterPersistedObjectFamily(family)
	}
}
