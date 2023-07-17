package engine

// shared consts between engine subpackages
const (
	DaggerFrontendName    = "dagger.v0"
	DaggerFrontendOptsKey = "dagger_frontend_opts"
	SessionIDHeader       = "X-Dagger-Session-ID"

	// session-related grpc labels
	ServerIDMetaKey           = "dagger-server-id"
	RequesterSessionIDMetaKey = "dagger-requester-session-id"
	// local dir import
	LocalDirImportDirNameMetaKey = "dir-name" // from buildkit
	// local dir export
	LocalDirExportDestSessionIDMetaKey = "dagger-local-dir-export-dest-session-id"
	LocalDirExportDestPathMetaKey      = "dagger-local-dir-export-dest-path"
	// worker label
	DaggerFrontendSessionIDLabel = "dagger-frontend-session-id"
)
