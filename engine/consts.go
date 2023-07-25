package engine

// shared consts between engine subpackages
// TODO: it's hella annoying to add one of these, just pass json structs around to make it easier
const (
	RouterIDMetaKey       = "x-dagger-router-id"
	ClientIDMetaKey       = "x-dagger-client-id"
	ClientHostnameMetaKey = "x-dagger-client-hostname"
	ClientLabelsMetaKey   = "x-dagger-client-labels"
	EngineVersionMetaKey  = "x-dagger-engine" // don't change, would be backwards incompatible

	// session API (these are set by buildkit, can't change)
	SessionIDMetaKey        = "x-docker-expose-session-uuid"
	SessionNameMetaKey      = "x-docker-expose-session-name"
	SessionSharedKeyMetaKey = "x-docker-expose-session-sharedkey"

	// local dir import
	LocalDirImportDirNameMetaKey = "dir-name" // from buildkit, can't change

	// local dir export
	LocalDirExportDestClientIDMetaKey       = "x-dagger-local-dir-export-dest-client-id"
	LocalDirExportDestPathMetaKey           = "x-dagger-local-dir-export-dest-path"
	LocalDirExportWriteStreamMetaKey        = "x-dagger-local-dir-export-write-stream"
	LocalDirExportIsFileMetaKey             = "x-dagger-local-dir-export-is-file"
	LocalDirExportAllowParentDirPathMetaKey = "x-dagger-local-dir-export-allow-parent-dir-path"
)
