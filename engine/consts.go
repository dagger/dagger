package engine

// shared consts between engine subpackages
// TODO: it's hella annoying to add one of these, just pass json structs around to make it easier
const (
	EngineVersionMetaKey = "x-dagger-engine" // don't change, would be backwards incompatible

	// session API (these are set by buildkit, can't change)
	SessionIDMetaKey        = "x-docker-expose-session-uuid"
	SessionNameMetaKey      = "x-docker-expose-session-name"
	SessionSharedKeyMetaKey = "x-docker-expose-session-sharedkey"

	// local dir import (non dagger-x-* keys are set by buildkit, can't change)
	LocalDirImportDirNameMetaKey         = "dir-name"
	LocalDirImportIncludePatternsMetaKey = "include-patterns"
	LocalDirImportExcludePatternsMetaKey = "exclude-patterns"
	LocalDirImportFollowPathsMetaKey     = "followpaths"
	LocalDirImportReadSingleFileMetaKey  = "x-dagger-local-dir-import-read-single-file"

	// local dir export
	LocalDirExportDestClientIDMetaKey       = "x-dagger-local-dir-export-dest-client-id"
	LocalDirExportDestPathMetaKey           = "x-dagger-local-dir-export-dest-path"
	LocalDirExportIsFileStreamMetaKey       = "x-dagger-local-dir-export-is-file-stream"
	LocalDirExportFileOriginalNameMetaKey   = "x-dagger-local-dir-export-file-original-name"
	LocalDirExportAllowParentDirPathMetaKey = "x-dagger-local-dir-export-allow-parent-dir-path"
)
