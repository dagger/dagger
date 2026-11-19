package core

// Workspace operations either declare a source read, record their nested work,
// or only transform values. Unclassified operations must not claim empty reads.
type workspaceInputOperationKind uint8

const (
	workspaceInputUnknownOperation workspaceInputOperationKind = iota
	workspaceInputObservedOperation
	workspaceInputReadOperation
)

func workspaceInputOperationClass(field string) workspaceInputOperationKind {
	switch field {
	case "file", "directory", "glob", "findUp", "search", "findRoots":
		return workspaceInputReadOperation
	// These operations also read through host or native helpers. Until their
	// effects have a narrower rule, retain their whole-workspace read bound.
	case "withInitialized", "migrate", "migrateModule", "compareCommitsFrom", "withCommitsFrom",
		"__pullDirectory", "__pullRepository", "__saveDirectory", "withCommit", "withReset", "withUserConfig",
		"withModule", "withoutModule", "withSdk", "withSDK", "withoutSdk", "withoutSDK",
		"withMain", "withEntrypoint", "withoutMain", "withoutEntrypoint", "withInitModule", "detectScope",
		"withClient", "withoutClient", "withUpdatedClients", "withConfigValue", "withoutConfigValue",
		"withConfigEnv", "withoutConfigEnv", "withUpdatedLock", "__withUpdatedLock", "__lockEntries",
		"withUpdatedModules", "sdks", "sdk", "changes", "export", "configRead", "main", "entrypoint",
		"envList", "modules", "module", "__doctor":
		return workspaceInputReadOperation
	case "resolve", "artifacts", "moduleSource":
		// A compound operation owns its input facts and completed body reads.
		// It must not suppress nested reads or recapture only prepared inputs
		// on a cache hit.
		return workspaceInputObservedOperation
	case "address", "cwd", "path", "configPath", "configFile", "hasConfig", "initialized",
		"__workspaceModule", "__workspaceSDK", "__withUserConfigOverlay",
		"git", "snapshot", "withWorkdir", "withConfigPaths", "withConfigEnvironment",
		"withNewFile", "withFile", "withNewDirectory", "withDirectory", "withoutFile", "withoutFiles", "withoutDirectory",
		"withChanges", "withPatchFile", "withMountedDirectory", "withMountedFile", "withoutMount":
		// Metadata and workspace views preserve their input origins and stored
		// capabilities without declaring that every source file was read.
		return workspaceInputObservedOperation
	default:
		return workspaceInputUnknownOperation
	}
}
