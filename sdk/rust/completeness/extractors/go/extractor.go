package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

const formatVersion = 1

// Request is the complete in-memory authority boundary supplied by Rust.
type Request struct {
	FormatVersion      int          `json:"format_version"`
	Files              []SourceFile `json:"files"`
	VersionLiteralName string       `json:"version_literal_name,omitempty"`
}

// SourceFile is one registered repository-relative Go file and its exact UTF-8 content.
type SourceFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Output is the normalized helper protocol consumed and revalidated by Rust.
type Output struct {
	FormatVersion   int    `json:"format_version"`
	Items           []Item `json:"items"`
	GoSDKLibVersion string `json:"go_sdk_lib_version,omitempty"`
}

// Item records one exported declaration or stable test identity.
type Item struct {
	Kind        string `json:"kind"`
	Package     string `json:"package"`
	Name        string `json:"name"`
	Receiver    string `json:"receiver,omitempty"`
	Parent      string `json:"parent,omitempty"`
	Signature   string `json:"signature"`
	State       string `json:"state"`
	Locator     string `json:"locator"`
	Fingerprint string `json:"fingerprint"`
}

// Extract parses only the request's exact files and returns stable, sorted output.
func Extract(request Request) (Output, error) {
	if request.FormatVersion != formatVersion {
		return Output{}, fmt.Errorf("unsupported format version %d", request.FormatVersion)
	}
	if len(request.Files) == 0 {
		return Output{}, fmt.Errorf("source bundle is empty")
	}
	files := append([]SourceFile(nil), request.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	for i := 1; i < len(files); i++ {
		if files[i-1].Path == files[i].Path {
			return Output{}, fmt.Errorf("duplicate source path %q", files[i].Path)
		}
	}

	fset := token.NewFileSet()
	var items []Item
	var versions []string
	for _, source := range files {
		if source.Path == "" || strings.HasPrefix(source.Path, "/") || hasParentComponent(source.Path) {
			return Output{}, fmt.Errorf("non-canonical source path %q", source.Path)
		}
		file, err := parser.ParseFile(fset, source.Path, source.Content, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return Output{}, fmt.Errorf("%s: %w", source.Path, err)
		}
		extracted, literal, err := extractFile(fset, source.Path, file, request.VersionLiteralName)
		if err != nil {
			return Output{}, err
		}
		items = append(items, extracted...)
		if literal != "" {
			versions = append(versions, literal)
		}
	}
	expectedVersions := 0
	if request.VersionLiteralName != "" {
		expectedVersions = 1
	}
	if len(versions) != expectedVersions {
		return Output{}, fmt.Errorf("version literal %q resolved %d times", request.VersionLiteralName, len(versions))
	}
	sort.Slice(items, func(i, j int) bool { return itemKey(items[i]) < itemKey(items[j]) })
	for i := 1; i < len(items); i++ {
		if itemKey(items[i-1]) == itemKey(items[i]) {
			return Output{}, fmt.Errorf("duplicate source item %q", itemKey(items[i]))
		}
	}
	output := Output{FormatVersion: formatVersion, Items: items}
	if len(versions) == 1 {
		output.GoSDKLibVersion = versions[0]
	}
	return output, nil
}

func extractFile(fset *token.FileSet, path string, file *ast.File, versionName string) ([]Item, string, error) {
	var items []Item
	var version string
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.GenDecl:
			for _, spec := range declaration.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					if ast.IsExported(spec.Name.Name) {
						signature, err := nodeString(fset, spec)
						if err != nil {
							return nil, "", err
						}
						items = append(items, newItem(fset, path, file.Name.Name, "type", spec.Name.Name, "", "", signature, firstDoc(spec.Doc, declaration.Doc), spec.Pos(), false))
					}
				case *ast.ValueSpec:
					for index, name := range spec.Names {
						if versionName != "" && name.Name == versionName {
							literal, err := stringLiteralAt(spec, index)
							if err != nil {
								return nil, "", fmt.Errorf("%s: %w", path, err)
							}
							if version != "" {
								return nil, "", fmt.Errorf("%s: duplicate version literal %q", path, versionName)
							}
							version = literal
						}
						if ast.IsExported(name.Name) {
							signature, err := nodeString(fset, spec)
							if err != nil {
								return nil, "", err
							}
							items = append(items, newItem(fset, path, file.Name.Name, strings.ToLower(declaration.Tok.String()), name.Name, "", "", signature, firstDoc(spec.Doc, declaration.Doc), name.Pos(), false))
						}
					}
				}
			}
		case *ast.FuncDecl:
			if strings.HasSuffix(path, "_test.go") && isTest(declaration) {
				signature, err := nodeString(fset, declaration.Type)
				if err != nil {
					return nil, "", err
				}
				receiver := receiverName(declaration.Recv)
				items = append(items, newItem(fset, path, file.Name.Name, "test", declaration.Name.Name, receiver, "", signature, declaration.Doc, declaration.Pos(), containsSkip(declaration.Body)))
				parent := declaration.Name.Name
				if receiver != "" {
					parent = receiver + "." + parent
				}
				subtests, err := extractSubtests(fset, path, file.Name.Name, parent, declaration)
				if err != nil {
					return nil, "", err
				}
				items = append(items, subtests...)
				continue
			}
			receiver := receiverName(declaration.Recv)
			if ast.IsExported(declaration.Name.Name) && (receiver == "" || ast.IsExported(receiver)) {
				signature, err := nodeString(fset, declaration.Type)
				if err != nil {
					return nil, "", err
				}
				kind := "function"
				if receiver != "" {
					kind = "method"
				}
				items = append(items, newItem(fset, path, file.Name.Name, kind, declaration.Name.Name, receiver, "", signature, declaration.Doc, declaration.Pos(), false))
			}
		}
	}
	return items, version, nil
}

func extractSubtests(fset *token.FileSet, path, packageName, parent string, function *ast.FuncDecl) ([]Item, error) {
	var items []Item
	var extractionErr error
	dynamicOccurrences := map[string]int{}
	literalOccurrences := map[string]int{}
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "Run" {
			return true
		}
		name, kind := "<dynamic>", "dynamic-subtest"
		dynamic := true
		if literal, ok := call.Args[0].(*ast.BasicLit); ok && literal.Kind == token.STRING {
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				extractionErr = err
				return false
			}
			name, kind = value, "subtest"
			dynamic = false
			occurrence := literalOccurrences[name]
			literalOccurrences[name] = occurrence + 1
			if occurrence > 0 {
				name = fmt.Sprintf("%s#%d", name, occurrence)
			}
		}
		signature, err := nodeString(fset, call)
		if err != nil {
			extractionErr = err
			return false
		}
		var tableName, tableSignature string
		if dynamic {
			tableSignature, err = nodeString(fset, call.Args[0])
			if err != nil {
				extractionErr = err
				return false
			}
			identity := signature + "\x00" + tableSignature
			occurrence := dynamicOccurrences[identity]
			dynamicOccurrences[identity] = occurrence + 1
			digest := sha256.Sum256([]byte(identity))
			shortDigest := hex.EncodeToString(digest[:8])
			name = fmt.Sprintf("<dynamic:%s:%d>", shortDigest, occurrence)
			tableName = fmt.Sprintf("<table:%s:%d>", shortDigest, occurrence)
		}
		skipped := false
		if functionLiteral, ok := call.Args[1].(*ast.FuncLit); ok {
			skipped = containsSkip(functionLiteral.Body)
		}
		items = append(items, newItem(fset, path, packageName, kind, name, "", parent, signature, nil, call.Pos(), skipped))
		if dynamic {
			items = append(items, newItem(fset, path, packageName, "test-table", tableName, "", parent, tableSignature, nil, call.Args[0].Pos(), false))
		}
		return true
	})
	return items, extractionErr
}

func newItem(fset *token.FileSet, path, packageName, kind, name, receiver, parent, signature string, doc *ast.CommentGroup, position token.Pos, skipped bool) Item {
	state := "active"
	if skipped {
		state = "skipped"
	} else if isDeprecated(doc) {
		state = "deprecated"
	}
	sum := sha256.Sum256([]byte(signature))
	location := fset.PositionFor(position, false)
	return Item{
		Kind: kind, Package: packageName, Name: name, Receiver: receiver, Parent: parent,
		Signature: signature, State: state,
		Locator:     fmt.Sprintf("%s:%d:%d", path, location.Line, location.Column),
		Fingerprint: "sha256:" + hex.EncodeToString(sum[:]),
	}
}

func nodeString(fset *token.FileSet, node any) (string, error) {
	var buffer bytes.Buffer
	if err := format.Node(&buffer, fset, node); err != nil {
		return "", fmt.Errorf("normalize syntax: %w", err)
	}
	return buffer.String(), nil
}

func stringLiteralAt(spec *ast.ValueSpec, index int) (string, error) {
	if len(spec.Values) == 0 {
		return "", fmt.Errorf("version declaration is not initialized")
	}
	valueIndex := index
	if len(spec.Values) == 1 {
		valueIndex = 0
	}
	if valueIndex >= len(spec.Values) {
		return "", fmt.Errorf("version declaration has no matching value")
	}
	literal, ok := spec.Values[valueIndex].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", fmt.Errorf("version declaration must be a string literal")
	}
	value, err := strconv.Unquote(literal.Value)
	if err != nil {
		return "", fmt.Errorf("invalid version string literal: %w", err)
	}
	return value, nil
}

func isTest(function *ast.FuncDecl) bool {
	if !strings.HasPrefix(function.Name.Name, "Test") ||
		function.Body == nil ||
		function.Type.Params == nil ||
		len(function.Type.Params.List) == 0 {
		return false
	}
	for _, parameter := range function.Type.Params.List {
		pointer, ok := parameter.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		selector, ok := pointer.X.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == "T" {
			return true
		}
	}
	return false
}

func containsSkip(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if ok && (selector.Sel.Name == "Skip" || selector.Sel.Name == "Skipf" || selector.Sel.Name == "SkipNow") {
			found = true
			return false
		}
		return true
	})
	return found
}

func receiverName(fields *ast.FieldList) string {
	if fields == nil || len(fields.List) == 0 {
		return ""
	}
	expression := fields.List[0].Type
	if star, ok := expression.(*ast.StarExpr); ok {
		expression = star.X
	}
	if indexed, ok := expression.(*ast.IndexExpr); ok {
		expression = indexed.X
	}
	if indexed, ok := expression.(*ast.IndexListExpr); ok {
		expression = indexed.X
	}
	if identifier, ok := expression.(*ast.Ident); ok {
		return identifier.Name
	}
	return ""
}

func isDeprecated(doc *ast.CommentGroup) bool {
	return doc != nil && strings.Contains(doc.Text(), "Deprecated:")
}

func firstDoc(groups ...*ast.CommentGroup) *ast.CommentGroup {
	for _, group := range groups {
		if group != nil {
			return group
		}
	}
	return nil
}

func itemKey(item Item) string {
	return strings.Join([]string{item.Kind, item.Package, item.Receiver, item.Parent, item.Name}, "\x00")
}

func hasParentComponent(path string) bool {
	for _, component := range strings.Split(path, "/") {
		if component == "" || component == "." || component == ".." {
			return true
		}
	}
	return false
}
