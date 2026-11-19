package core

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/dagger/dagger/core/provenance"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

// A Host search has a declared Workspace meaning for path filtering only.
// Keep the rule bound to its caller; inspecting a stored value must not bind
// it to the inspector's Workspace.
type workspaceFindUpRule struct {
	Name      string `json:"name"`
	Workspace string `json:"workspace"`
	Cwd       string `json:"cwd"`
}

func workspaceFindUpSelectors(name, cwd, from, origin string) ([]provenance.Selector, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return nil, fmt.Errorf("findUp name is not a basename")
	}
	from = strings.ReplaceAll(from, `\`, "/")
	if from == "" {
		from = "."
	}
	if !path.IsAbs(from) {
		from = path.Join(cwd, from)
	}
	start, err := provenance.Path(from)
	if err != nil {
		return nil, err
	}
	// Include the basename at each ancestor, and exclude its children. A
	// selector with just Path would also select descendants of that path.
	pattern := strings.NewReplacer("*", `\*`, "?", `\?`, "[", `\[`, "]", `\]`, "!", `\!`).Replace(name)
	var selectors []provenance.Selector
	for current := start; ; current = path.Dir(current) {
		selectors = append(selectors, provenance.Selector{
			Path: current, Include: []string{pattern}, Exclude: []string{pattern + "/*"},
			Kind: provenance.WorkspaceRead, Origin: origin,
		})
		if current == "/" {
			return selectors, nil
		}
	}
}

func (rule workspaceFindUpRule) source() workspaceInputSource {
	selectors, err := workspaceFindUpSelectors(rule.Name, rule.Cwd, ".", "Host.findUp")
	source := workspaceInputSource{Workspace: rule.Workspace, Selectors: selectors, KnownBoundary: rule.Workspace != ""}
	if err != nil {
		source.Issues = []string{"cannot resolve Host.findUp filtering paths: " + err.Error()}
	}
	return source
}

// Only the effective client's selected Workspace defines this Host rule.
// WorkspaceToContext can name a different source and must not retarget it.
func bindWorkspaceFindUps(ctx context.Context, rules []workspaceFindUpRule) ([]workspaceFindUpRule, error) {
	if len(rules) == 0 {
		return nil, nil
	}
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if scope, ok := engine.ClientScopeFromContext(ctx); ok && scope.ClientID() != md.ClientID {
		return nil, fmt.Errorf("effective client %q differs from calling client %q; caller Workspace is unavailable", md.ClientID, scope.ClientID())
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	ws, err := query.Server.CurrentWorkspace(ctx)
	if err != nil {
		return nil, err
	}
	boundary, err := actualWorkspaceBoundary(ctx, ws)
	if err != nil {
		return nil, err
	}
	out := slices.Clone(rules)
	for i := range out {
		out[i].Workspace = boundary
		out[i].Cwd = ws.Cwd
	}
	return out, nil
}

func (c *workspaceInputCollector) hostFindUp(req *dagql.CallRequest) {
	name := req.Arg("name")
	if name == nil || name.Value == nil || name.Value.Kind != dagql.ResultCallLiteralKindString {
		c.hostIssue(req, "findUp filtering rule has no basename")
		return
	}
	if _, err := workspaceFindUpSelectors(name.Value.StringValue, ".", ".", ""); err != nil {
		c.hostIssue(req, "findUp filtering rule has no valid basename")
		return
	}
	rules, err := bindWorkspaceFindUps(c.ctx, []workspaceFindUpRule{{Name: name.Value.StringValue}})
	if err != nil {
		c.hostIssue(req, "findUp filtering rule has no caller Workspace: "+err.Error())
		return
	}
	c.sourcePreparation = c.result.normalized()
	c.result.FindUps = append(c.result.FindUps, rules...)
	c.result.findUps = append(c.result.findUps, rules...)
}

func (observation *workspaceInputCall) rebindFindUps(ctx context.Context, rules []workspaceFindUpRule) []workspaceFindUpRule {
	bound, err := bindWorkspaceFindUps(ctx, rules)
	if err != nil {
		observation.reads.add(workspaceInputSummary{Issues: []string{"cannot bind Host.findUp filtering rule to caller Workspace: " + err.Error()}, incomplete: true})
	}
	return bound
}
