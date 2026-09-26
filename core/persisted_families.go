package core

import "github.com/dagger/dagger/dagql"

// persistedObjectFamilies is the inventory of every persisted object codec
// declared by this package: the Go payload family written into object
// envelopes, the Go type whose codec produces it, and the pure reference
// visitor that walks its declared references without constructing the value.
// Registration happens at initialization, independently of which schemas a
// session installs. A codec without a family cannot be encoded, so an
// unfinished type fails loudly instead of persisting an unreadable payload.
// Every family listed with BackgroundDecode is in the design's admitted
// decoder audit: its DecodePersistedObject and the helpers it calls
// reconstruct data and load exact persisted references only. None creates a
// client, resolves a secret, socket or SDK, starts or mounts anything,
// evaluates a schema or evaluates a lazy operation. A family absent from that
// audit stays false, and a receiver whose decode closure needs it is
// ineligible for early sharing rather than entering a shared attempt.
var persistedObjectFamilies = []dagql.PersistedObjectFamily{
	// Filesystems and resources.
	{Name: "core.Container", Typed: (*Container)(nil), Visitor: persistedContainerVisitor, Transfer: foreignFamilyCodec("Container"), BackgroundDecode: true},
	{Name: "core.Directory", Typed: (*Directory)(nil), Visitor: persistedDirectoryVisitor, Transfer: foreignFamilyCodec("Directory"), BackgroundDecode: true},
	{Name: "core.File", Typed: (*File)(nil), Visitor: persistedFileVisitor, Transfer: foreignFamilyCodec("File"), BackgroundDecode: true},
	{Name: "core.Service", Typed: (*Service)(nil), Visitor: persistedServiceVisitor, BackgroundDecode: true},
	{Name: "core.Volume", Typed: (*Volume)(nil), Visitor: persistedVolumeVisitor, BackgroundDecode: true},
	{Name: "core.Secret", Typed: (*Secret)(nil), Visitor: dagql.PersistedNoReferences{}, BackgroundDecode: true},
	{Name: "core.Socket", Typed: (*Socket)(nil), Visitor: dagql.PersistedNoReferences{}, BackgroundDecode: true},
	{Name: "core.CacheVolume", Typed: (*CacheVolume)(nil), Visitor: persistedCacheVolumeVisitor, Transfer: foreignFamilyCodec("CacheVolume"), BackgroundDecode: true},
	{Name: "core.ClientFilesyncMirror", Typed: (*ClientFilesyncMirror)(nil), Visitor: persistedClientFilesyncMirrorVisitor, Transfer: foreignFamilyCodec("ClientFilesyncMirror")},
	{Name: "core.RemoteGitMirror", Typed: (*RemoteGitMirror)(nil), Visitor: persistedRemoteGitMirrorVisitor, Transfer: foreignFamilyCodec("RemoteGitMirror"), BackgroundDecode: true},

	// Module, workspace and action state.
	{Name: "core.Module", Typed: (*Module)(nil), Visitor: persistedModuleVisitor, BackgroundDecode: true},
	{Name: "core.ModuleSource", Typed: (*ModuleSource)(nil), Visitor: persistedModuleSourceVisitor, Transfer: foreignFamilyCodec("ModuleSource"), BackgroundDecode: true},
	{Name: "core.ModuleObject", Typed: (*ModuleObject)(nil), Visitor: persistedModuleObjectVisitor},
	{Name: "core.Workspace", Typed: (*Workspace)(nil), Visitor: persistedWorkspaceVisitor, BackgroundDecode: true},
	{Name: "core.WorkspaceGit", Typed: (*WorkspaceGit)(nil), Visitor: persistedWorkspaceGitVisitor, BackgroundDecode: true},
	{Name: "core.WorkspaceModule", Typed: (*WorkspaceModule)(nil), Visitor: dagql.PersistedNoReferences{}, BackgroundDecode: true},
	{Name: "core.WorkspaceModuleSetting", Typed: (*WorkspaceModuleSetting)(nil), Visitor: dagql.PersistedNoReferences{}, BackgroundDecode: true},
	{Name: "core.WorkspaceSDK", Typed: (*WorkspaceSDK)(nil), Visitor: persistedWorkspaceSDKVisitor, BackgroundDecode: true},
	{Name: "core.Artifact", Typed: (*Artifact)(nil), Visitor: persistedArtifactsVisitor},
	{Name: "core.Artifacts", Typed: (*Artifacts)(nil), Visitor: persistedArtifactsVisitor},
	{Name: "core.Expertise", Typed: (*Expertise)(nil), Visitor: persistedArtifactsVisitor},

	// Immutable outputs.
	{Name: "core.GitRepository", Typed: (*GitRepository)(nil), Visitor: persistedGitRepositoryVisitor, BackgroundDecode: true},
	{Name: "core.GitRef", Typed: (*GitRef)(nil), Visitor: persistedGitRefVisitor, BackgroundDecode: true},
	{Name: "core.GitCommit", Typed: (*GitCommit)(nil), Visitor: persistedGitCommitVisitor, BackgroundDecode: true},
	{Name: "core.GitBundle", Typed: (*GitBundle)(nil), Visitor: persistedGitBundleVisitor},
	{Name: "core.HTTPState", Typed: (*HTTPState)(nil), Visitor: persistedHTTPStateVisitor, Transfer: foreignFamilyCodec("HTTPState")},
	{Name: "core.Changeset", Typed: (*Changeset)(nil), Visitor: persistedChangesetVisitor, BackgroundDecode: true},
	{Name: "core.GeneratedCode", Typed: (*GeneratedCode)(nil), Visitor: persistedGeneratedCodeVisitor},
	{Name: "core.SearchResult", Typed: (*SearchResult)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.SearchSubmatch", Typed: (*SearchSubmatch)(nil), Visitor: dagql.PersistedNoReferences{}},

	// Definitions.
	{Name: "core.Function", Typed: (*Function)(nil), Visitor: persistedFunctionVisitor, BackgroundDecode: true},
	{Name: "core.FunctionArg", Typed: (*FunctionArg)(nil), Visitor: persistedFunctionArgVisitor, BackgroundDecode: true},
	{Name: "core.TypeDef", Typed: (*TypeDef)(nil), Visitor: persistedTypeDefVisitor, BackgroundDecode: true},
	{Name: "core.ObjectTypeDef", Typed: (*ObjectTypeDef)(nil), Visitor: persistedObjectTypeDefVisitor, BackgroundDecode: true},
	{Name: "core.FieldTypeDef", Typed: (*FieldTypeDef)(nil), Visitor: persistedFieldTypeDefVisitor, BackgroundDecode: true},
	{Name: "core.InterfaceTypeDef", Typed: (*InterfaceTypeDef)(nil), Visitor: persistedInterfaceTypeDefVisitor, BackgroundDecode: true},
	{Name: "core.ScalarTypeDef", Typed: (*ScalarTypeDef)(nil), Visitor: dagql.PersistedNoReferences{}, BackgroundDecode: true},
	{Name: "core.ListTypeDef", Typed: (*ListTypeDef)(nil), Visitor: persistedListTypeDefVisitor, BackgroundDecode: true},
	{Name: "core.InputTypeDef", Typed: (*InputTypeDef)(nil), Visitor: persistedInputTypeDefVisitor, BackgroundDecode: true},
	{Name: "core.EnumTypeDef", Typed: (*EnumTypeDef)(nil), Visitor: persistedEnumTypeDefVisitor, BackgroundDecode: true},
	{Name: "core.EnumMemberTypeDef", Typed: (*EnumMemberTypeDef)(nil), Visitor: persistedEnumMemberTypeDefVisitor, BackgroundDecode: true},

	// Data and reflection.
	{Name: "core.Address", Typed: (*Address)(nil), Visitor: persistedAddressVisitor},
	{Name: "core.Host", Typed: (*Host)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.EnvFile", Typed: (*EnvFile)(nil), Visitor: dagql.PersistedNoReferences{}, BackgroundDecode: true},
	{Name: "core.JSONValue", Typed: (*JSONValue)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.Error", Typed: (*Error)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.ErrorValue", Typed: (*ErrorValue)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.DiffStat", Typed: (*DiffStat)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.Stat", Typed: (*Stat)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.FunctionCall", Typed: (*FunctionCall)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.FunctionCallArgValue", Typed: (*FunctionCallArgValue)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.SourceMap", Typed: (*SourceMap)(nil), Visitor: dagql.PersistedNoReferences{}, BackgroundDecode: true},
	{Name: "core.LLMTokenUsage", Typed: (*LLMTokenUsage)(nil), Visitor: dagql.PersistedNoReferences{}},
	{Name: "core.LLMVariable", Typed: (*LLMVariable)(nil), Visitor: dagql.PersistedNoReferences{}},
}

func init() {
	for _, family := range persistedObjectFamilies {
		dagql.RegisterPersistedObjectFamily(family)
	}
}
