package core

import (
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

// hostCall describes the operation, not the Host handle's capabilities. Host
// calls inside a Workspace read are already covered by that read's boundary.
func (c *workspaceInputCollector) hostCall(req *dagql.CallRequest) {
	c.inputs(req.ResultCall)
	switch req.Field {
	case "unixSocket", "_sshAuthSocket", "service", "tunnel":
		// These operations connect to an external service or authentication
		// agent. They do not read source files. Explicit inputs still count.
		return
	case "containerImage":
		// The image comes from the client's image store, not a source tree.
		return
	case "file", "directory", "findUp", "__gitDir":
		if c.runtimeHostInputs() {
			return
		}
		if req.Field == "findUp" {
			c.hostFindUp(req)
			return
		}
		c.hostIssue(req, "source input has no workspace source mapping")
	default:
		c.hostIssue(req, "operation has unknown source effects")
	}
}

// Only the exact executing module client can read from this runtime. Workspace
// owner routing changes metadata without changing the held execution scope.
func (c *workspaceInputCollector) runtimeHostInputs() bool {
	md, err := engine.ClientMetadataFromContext(c.ctx)
	if err != nil {
		return false
	}
	scope, ok := engine.ClientScopeFromContext(c.ctx)
	if !ok || scope.ClientID() != md.ClientID {
		return false
	}
	query, err := CurrentQuery(c.ctx)
	if err != nil {
		return false
	}
	call, err := query.CurrentFunctionCall(c.ctx)
	if err != nil || call == nil || call.workspaceRuntimeInputs == nil {
		return false
	}
	c.result.merge(*call.workspaceRuntimeInputs)
	return true
}

func (c *workspaceInputCollector) hostIssue(req *dagql.CallRequest, issue string) {
	md, err := engine.ClientMetadataFromContext(c.ctx)
	if err != nil {
		c.issue("Host.%s %s (cannot inspect effective client: %v)", req.Field, issue, err)
		return
	}
	if scope, ok := engine.ClientScopeFromContext(c.ctx); ok {
		c.issue("Host.%s %s (effective client %q, calling client %q)", req.Field, issue, md.ClientID, scope.ClientID())
		return
	}
	c.issue("Host.%s %s (effective client %q)", req.Field, issue, md.ClientID)
}
