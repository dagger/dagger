package engine

// shared consts between engine subpackages
const (
	RouterIDMetaKey      = "x-dagger-router-id"
	ClientIDMetaKey      = "x-dagger-requester-client-id"
	EngineVersionMetaKey = "x-dagger-engine" // don't change, would be backwards incompatible

	// session API (these are set by buildkit, can't change)
	SessionIDMetaKey   = "x-docker-expose-session-uuid"
	SessionNameMetaKey = "x-docker-expose-session-name"

	// local dir import
	LocalDirImportDirNameMetaKey = "dir-name" // from buildkit, can't change

	// local dir export
	LocalDirExportDestClientIDMetaKey = "x-dagger-local-dir-export-dest-client-id"
	LocalDirExportDestPathMetaKey     = "x-dagger-local-dir-export-dest-path"
)
