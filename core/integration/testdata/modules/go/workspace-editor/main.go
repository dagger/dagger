// A module that edits a file in the workspace it was handed, so the resulting
// changeset is produced from inside a module function rather than by the
// client that owns the checkout.
//
// Used by core/integration/workspace_module_edit_test.go, the confirmation
// experiment for hack/designs/async-agents.md §11 item 14 ("Changeset replay
// loses tracked-ness"): a module-held workspace editing a host-present file
// that the parent overlay never touched.
package main

import (
	"context"
	"fmt"
	"strings"

	"dagger/editor/internal/dagger"
)

type Editor struct{}

// EditLine rewrites a single line of an existing workspace file in place: read
// it through the workspace, replace one line, write it back. The workspace
// argument is auto-injected with the caller's workspace when the caller is a
// client rather than another module.
func (m *Editor) EditLine(
	ctx context.Context,
	ws *dagger.Workspace,
	// Workspace-relative path of the file to edit.
	path string,
	// Line to replace, which must be present.
	oldLine string,
	// Replacement line.
	newLine string,
) (*dagger.Workspace, error) {
	return editLine(ctx, ws, path, oldLine, newLine)
}

// TouchThenEditLine writes an unrelated file first, then performs the same
// surgical edit. The unrelated write gives the workspace an overlay whose
// accumulated touched path set does NOT contain the edited path, which is the
// configuration item 14 predicts will report the edit as a whole-file add.
func (m *Editor) TouchThenEditLine(
	ctx context.Context,
	ws *dagger.Workspace,
	// Workspace-relative path of the file to edit.
	path string,
	// Line to replace, which must be present.
	oldLine string,
	// Replacement line.
	newLine string,
	// Workspace-relative path of the unrelated file to write first.
	scratch string,
) (*dagger.Workspace, error) {
	return editLine(ctx, ws.WithNewFile(scratch, "scratch\n"), path, oldLine, newLine)
}

func editLine(ctx context.Context, ws *dagger.Workspace, path, oldLine, newLine string) (*dagger.Workspace, error) {
	contents, err := ws.File(path).Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if !strings.Contains(contents, oldLine) {
		return nil, fmt.Errorf("%s does not contain %q", path, oldLine)
	}
	return ws.WithNewFile(path, strings.Replace(contents, oldLine, newLine, 1)), nil
}
