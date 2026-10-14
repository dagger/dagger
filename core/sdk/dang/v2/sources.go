package dangv2

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vito/dang/v2/pkg/dang"
)

// EntrypointSourcesMember is the optional entrypoint member that returns the
// files the module builds from. Declaring it selects the entrypoint contract
// in which types and call receive no workspace.
const EntrypointSourcesMember = "sources"

// DeclaresEntrypointSources reports whether a type that implements
// ModuleEntrypoint in the top-level Dang files of dir declares a public
// sources member. A comment, a string, a private member or a member of another
// type does not count. A file that does not parse declares nothing, so its
// error surfaces when the entrypoint runs.
func DeclaresEntrypointSources(dir string) (bool, error) {
	files, err := entrypointFiles(dir)
	if err != nil {
		return false, err
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return false, fmt.Errorf("read module entrypoint file %q: %w", filepath.Base(path), err)
		}
		if !bytes.Contains(data, []byte(EntrypointSourcesMember)) {
			continue
		}
		root, err := dang.Parse(path, data)
		if err != nil {
			return false, nil //nolint:nilerr
		}
		if file, ok := root.(*dang.FileBlock); ok && declaresSources(file) {
			return true, nil
		}
	}
	return false, nil
}

// entrypointFiles lists the files that runEntrypointDir runs.
func entrypointFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read module entrypoint directory: %w", err)
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".dang" {
			continue
		}
		files = append(files, filepath.Join(dir, entry.Name()))
	}
	return files, nil
}

func declaresSources(file *dang.FileBlock) bool {
	for _, form := range file.Forms {
		decl, ok := form.(*dang.ObjectDecl)
		if !ok || decl.Value == nil || !implementsModuleEntrypoint(decl) {
			continue
		}
		for _, member := range decl.Value.Forms {
			field, ok := member.(*dang.FieldDecl)
			if ok && field.Name != nil && field.Name.Name == EntrypointSourcesMember && field.Visibility >= dang.PublicVisibility {
				return true
			}
		}
	}
	return false
}

func implementsModuleEntrypoint(decl *dang.ObjectDecl) bool {
	for _, iface := range decl.Implements {
		if iface != nil && iface.Name == moduleEntrypointTypeName {
			return true
		}
	}
	return false
}
