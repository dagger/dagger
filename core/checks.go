package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/util/parallel"
	"github.com/vektah/gqlparser/v2/ast"
)

// Check represents a validation check with its result
type Check struct {
	Node      *ModTreeNode `json:"node"`
	Completed bool         `field:"true" doc:"Whether the check completed"`
	Passed    bool         `field:"true" doc:"Whether the check passed"`
}

type CheckGroup struct {
	Node   *ModTreeNode `json:"node"`
	Checks []*Check     `json:"checks"`
}

func NewCheckGroup(ctx context.Context, mod *Module, include []string, all bool) (*CheckGroup, error) {
	rootNode, err := NewModTree(ctx, mod)
	if err != nil {
		return nil, err
	}

	var exclude []string
	for toolchainName, toolchainIgnorePatterns := range mod.ToolchainIgnoreChecks {
		for _, ignorePattern := range toolchainIgnorePatterns {
			exclude = append(exclude, toolchainName+":"+ignorePattern)
		}
	}
	checkNodes, err := rootNode.RollupChecks(ctx, include, exclude, all)
	if err != nil {
		return nil, err
	}
	checks := make([]*Check, 0, len(checkNodes))

	for _, checkNode := range checkNodes {
		checks = append(checks, &Check{Node: checkNode})
	}
	return &CheckGroup{
		Node:   rootNode,
		Checks: checks,
	}, nil
}

func (*CheckGroup) Type() *ast.Type {
	return &ast.Type{
		NamedType: "CheckGroup",
		NonNull:   true,
	}
}

func (r *CheckGroup) List() []*Check {
	return r.Checks
}

// Run all the checks in the group
func (r *CheckGroup) Run(ctx context.Context) (*CheckGroup, error) {
	r = r.Clone()

	jobs := parallel.New().WithContextualTracer(true)
	for _, check := range r.Checks {
		// Reset output fields, in case we're re-running
		check.Completed = false
		check.Passed = false
		jobs = jobs.WithJob(check.Name(), func(ctx context.Context) error {
			err := check.Node.RunCheck(ctx, nil, nil)
			check.Completed = true
			check.Passed = (err == nil)
			return err
		})
	}
	if err := jobs.Run(ctx); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *CheckGroup) Report(ctx context.Context) (*File, error) {
	headers := []string{"check", "description", "success"}
	rows := [][]string{}
	for _, check := range r.Checks {
		rows = append(rows, []string{
			check.Name(),
			check.Description(),
			check.ResultEmoji(),
		})
	}
	contents := []byte(markdownTable(headers, rows...))
	q, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	return NewFileWithContents(ctx, "checks.md", contents, fs.FileMode(0644), nil, q.Platform())
}

func markdownTable(headers []string, rows ...[]string) string {
	var sb strings.Builder
	sb.WriteString("| " + strings.Join(headers, " | ") + " |\n")
	for range headers {
		sb.WriteString("| -- ")
	}
	sb.WriteString("|\n")
	for _, row := range rows {
		sb.WriteString("|" + strings.Join(row, " | ") + " |\n")
	}
	return sb.String()
}

func (r *CheckGroup) Clone() *CheckGroup {
	cp := *r
	cp.Node = cp.Node.Clone()
	for i := range cp.Checks {
		cp.Checks[i] = cp.Checks[i].Clone()
	}
	return &cp
}

func (c *Check) Path() []string {
	return c.Node.Path()
}

func (c *Check) Description() string {
	return c.Node.Description
}

func (*Check) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Check",
		NonNull:   true,
	}
}

func (c *Check) ResultEmoji() string {
	if c.Completed {
		if c.Passed {
			return "🟢"
		}
		return "🔴"
	}
	return ""
}

func (c *Check) Name() string {
	return c.Node.PathString()
}

func (c *Check) Clone() *Check {
	cp := *c
	cp.Node = c.Node.Clone()
	return &cp
}

func (c *Check) Run(ctx context.Context) (*Check, error) {
	c = c.Clone()

	err := c.Node.RunCheck(ctx, nil, nil)
	c.Completed = true
	c.Passed = (err == nil)
	return c, nil
}

func (c *Check) tryScaleOut(ctx context.Context) (_ bool, rerr error) {
	q, err := CurrentQuery(ctx)
	if err != nil {
		return true, err
	}

	cloudEngineClient, useCloudEngine, err := q.CloudEngineClient(ctx,
		c.Node.RootAddress(),
		// FIXME: we're saying the "function" is the check and no execCmd,
		// which works with cloud but is weird
		c.Name(),
		nil,
	)
	if err != nil {
		return true, fmt.Errorf("engine-to-engine connect: %w", err)
	}
	if !useCloudEngine {
		// just run locally
		return false, nil
	}
	defer func() {
		rerr = errors.Join(rerr, cloudEngineClient.Close())
	}()

	query := cloudEngineClient.Dagger().QueryBuilder()

	//
	// construct a query to run this check on the cloud engine
	//

	// load the module, depending on its kind
	mod := c.Node.Module
	switch mod.Source.Value.Self().Kind {
	case ModuleSourceKindLocal:
		query = query.Select("moduleSource").
			Arg("refString", filepath.Join(
				mod.Source.Value.Self().Local.ContextDirectoryPath,
				mod.Source.Value.Self().SourceRootSubpath,
			))
	case ModuleSourceKindGit:
		query = query.Select("moduleSource").
			Arg("refString", mod.Source.Value.Self().AsString()).
			Arg("refPin", mod.Source.Value.Self().Git.Commit).
			Arg("requireKind", mod.Source.Value.Self().Kind)
	case ModuleSourceKindDir:
		// FIXME: whether this actually works or not depends on whether the dir is reproducible. For simplicity,
		// we just assume it is and will error out later if not. Would be better to explicitly check though.
		dirID := mod.Source.Value.Self().DirSrc.OriginalContextDir.ID()
		dirIDEnc, err := dirID.Encode()
		if err != nil {
			return true, fmt.Errorf("encode dir ID: %w", err)
		}
		query = query.Select("loadDirectoryFromID").
			Arg("id", dirIDEnc)
		query = query.Select("asModuleSource").
			Arg("sourceRootPath", mod.Source.Value.Self().DirSrc.OriginalSourceRootSubpath)
	}
	query = query.Select("asModule")

	// run the check

	query = query.Select("check").
		Arg("name", c.Name())

	query = query.Select("run")

	query = query.SelectMultiple("completed", "passed")

	// execute the query against the remote engine

	var res struct {
		Completed bool
		Passed    bool
	}
	err = query.Bind(&res).Execute(ctx)
	if err != nil {
		return true, err
	}

	return true, nil
}
