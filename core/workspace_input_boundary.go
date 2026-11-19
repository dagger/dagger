package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/dagger/dagger/dagql"
)

// A boundary identifies a logical source and is stored on its producer,
// independently of equal payloads and client routes. Native views carry their
// actual input boundary.
func (c *workspaceInputCollector) workspaceBoundary(ref *dagql.ResultCallRef) (string, error) {
	if ref == nil || ref.ResultID == 0 {
		return "", fmt.Errorf("Workspace source boundary has no attached producer")
	}
	frame, err := c.cache.ResultCallForRef(ref)
	if err != nil {
		return "", err
	}
	if frame == nil {
		return "", fmt.Errorf("Workspace source boundary has no producer operation")
	}
	operation := workspaceInputOperation(c.ctx, frame)
	facts, complete, err := c.cache.ResultFacts(context.WithoutCancel(c.ctx), c.session, ref, workspaceInputsNamespace)
	if err != nil {
		return "", err
	}
	if !complete {
		c.issue("Workspace input has incomplete source origins for %s", operation)
	}
	var boundary string
	for _, record := range facts {
		var fact workspaceInputFact
		if json.Unmarshal(record.Payload, &fact) != nil || fact.Operation != operation || !fact.CapabilitiesComplete || fact.Boundary == "" {
			continue
		}
		if !fact.Complete {
			c.issue("Workspace input has incomplete source query capture for %s", operation)
		}
		if boundary != "" && boundary != fact.Boundary {
			return "", fmt.Errorf("Workspace producer has ambiguous source boundaries for %s", operation)
		}
		boundary = fact.Boundary
	}
	if boundary != "" {
		return boundary, nil
	}
	return "", fmt.Errorf("Workspace producer has no complete source boundary for %s", operation)
}

func workspaceResultBoundary(ctx context.Context, workspace dagql.ObjectResult[*Workspace]) (string, error) {
	c, err := newWorkspaceInputCollector(ctx)
	if err != nil {
		return "", err
	}
	id, err := workspace.ID()
	if err != nil {
		return "", err
	}
	return c.workspaceBoundary(&dagql.ResultCallRef{ResultID: id.EngineResultID()})
}

func (c *workspaceInputCollector) sourceBoundary(ws *Workspace) (string, error) {
	if c.sourceProducer != nil {
		return c.workspaceBoundary(c.sourceProducer)
	}
	if workspace, boundary := workspaceInputSourceOrigin(c.ctx); workspace == ws && boundary != "" {
		return boundary, nil
	}
	return "", fmt.Errorf("source Workspace has no bound producer")
}

// The current server source is read before cached payload reuse. A local
// source's owner and address identify its filesystem. A directory source needs
// its actual producing input; equal directory contents are not source identity.
func actualWorkspaceBoundary(ctx context.Context, ws *Workspace) (string, error) {
	if ws == nil || ws.Address == "" {
		return "", fmt.Errorf("current Workspace has no source address")
	}
	switch source := ws.BaseSource().(type) {
	case *WorkspaceSourceGitRef:
		return "git-workspace:" + ws.Address, nil
	case *WorkspaceSourceDirectory:
		frame, err := source.Root.ResultCall()
		if err != nil {
			return "", err
		}
		recipe, err := frame.RecipeDigest(ctx)
		if err != nil {
			return "", err
		}
		return "directory-workspace:" + ws.Address + ":" + recipe.String(), nil
	default:
		if ws.ClientID == "" {
			return "", fmt.Errorf("current Workspace source has no owner")
		}
		return "client-workspace:" + workspaceInputKey(ws), nil
	}
}
