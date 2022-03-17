// Copyright 2018 The CUE Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package load

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/build"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/parser"
	"cuelang.org/go/cue/token"
	"cuelang.org/go/internal"
	"cuelang.org/go/internal/filetypes"
)

// An importMode controls the behavior of the Import method.
type importMode uint

const (
	// If findOnly is set, Import stops after locating the directory
	// that should contain the sources for a package. It does not
	// read any files in the directory.
	findOnly importMode = 1 << iota

	// If importComment is set, parse import comments on package statements.
	// Import returns an error if it finds a comment it cannot understand
	// or finds conflicting comments in multiple source files.
	// See golang.org/s/go14customimport for more information.
	importComment

	allowAnonymous
)

// importPkg returns details about the CUE package named by the import path,
// interpreting local import paths relative to the srcDir directory.
// If the path is a local import path naming a package that can be imported
// using a standard import path, the returned package will set p.ImportPath
// to that path.
//
// In the directory and ancestor directories up to including one with a
// cue.mod file, all .cue files are considered part of the package except for:
//
//	- files starting with _ or . (likely editor temporary files)
//	- files with build constraints not satisfied by the context
//
// If an error occurs, importPkg sets the error in the returned instance,
// which then may contain partial information.
//
// pkgName indicates which packages to load. It supports the following
// values:
//     ""      the default package for the directory, if only one
//             is present.
//     _       anonymous files (which may be marked with _)
//     *       all packages
//
func (l *loader) importPkg(pos token.Pos, p *build.Instance) []*build.Instance {
	l.stk.Push(p.ImportPath)
	defer l.stk.Pop()

	cfg := l.cfg
	ctxt := &cfg.fileSystem

	if p.Err != nil {
		return []*build.Instance{p}
	}

	retErr := func(errs errors.Error) []*build.Instance {
		// XXX: move this loop to ReportError
		for _, err := range errors.Errors(errs) {
			p.ReportError(err)
		}
		return []*build.Instance{p}
	}

	if !strings.HasPrefix(p.Dir, cfg.ModuleRoot) {
		err := errors.Newf(token.NoPos, "module root not defined", p.DisplayPath)
		return retErr(err)
	}

	fp := newFileProcessor(cfg, p)

	if p.PkgName == "" {
		if l.cfg.Package == "*" {
			fp.ignoreOther = true
			fp.allPackages = true
			p.PkgName = "_"
		} else {
			p.PkgName = l.cfg.Package
		}
	}
	if p.PkgName != "" {
		// If we have an explicit package name, we can ignore other packages.
		fp.ignoreOther = true
	}

	if !strings.HasPrefix(p.Dir, cfg.ModuleRoot) {
		panic("")
	}

	var dirs [][2]string
	genDir := GenPath(cfg.ModuleRoot)
	if strings.HasPrefix(p.Dir, genDir) {
		dirs = append(dirs, [2]string{genDir, p.Dir})
		// TODO(legacy): don't support "pkg"
		// && p.PkgName != "_"
		if filepath.Base(genDir) != "pkg" {
			for _, sub := range []string{"pkg", "usr"} {
				rel, err := filepath.Rel(genDir, p.Dir)
				if err != nil {
					// should not happen
					return retErr(
						errors.Wrapf(err, token.NoPos, "invalid path"))
				}
				base := filepath.Join(cfg.ModuleRoot, modDir, sub)
				dir := filepath.Join(base, rel)
				dirs = append(dirs, [2]string{base, dir})
			}
		}
	} else {
		dirs = append(dirs, [2]string{cfg.ModuleRoot, p.Dir})
	}

	found := false
	for _, d := range dirs {
		info, err := ctxt.stat(d[1])
		if err == nil && info.IsDir() {
			found = true
			break
		}
	}

	if !found {
		return retErr(
			&PackageError{
				Message: errors.NewMessage("cannot find package %q",
					[]interface{}{p.DisplayPath}),
			})
	}

	// This algorithm assumes that multiple directories within cue.mod/*/
	// have the same module scope and that there are no invalid modules.
	inModule := false // if pkg == "_"
	for _, d := range dirs {
		if l.cfg.findRoot(d[1]) != "" {
			inModule = true
			break
		}
	}

	for _, d := range dirs {
		for dir := filepath.Clean(d[1]); ctxt.isDir(dir); {
			files, err := ctxt.readDir(dir)
			if err != nil && !os.IsNotExist(err) {
				return retErr(errors.Wrapf(err, pos, "import failed reading dir %v", dirs[0][1]))
			}
			for _, f := range files {
				if f.IsDir() {
					continue
				}
				if f.Name() == "-" {
					if _, err := cfg.fileSystem.stat("-"); !os.IsNotExist(err) {
						continue
					}
				}
				file, err := filetypes.ParseFile(f.Name(), filetypes.Input)
				if err != nil {
					p.UnknownFiles = append(p.UnknownFiles, &build.File{
						Filename:      f.Name(),
						ExcludeReason: errors.Newf(token.NoPos, "unknown filetype"),
					})
					continue // skip unrecognized file types
				}
				fp.add(pos, dir, file, importComment)
			}

			if p.PkgName == "" || !inModule || l.cfg.isRoot(dir) || dir == d[0] {
				break
			}

			// From now on we just ignore files that do not belong to the same
			// package.
			fp.ignoreOther = true

			parent, _ := filepath.Split(dir)
			parent = filepath.Clean(parent)

			if parent == dir || len(parent) < len(d[0]) {
				break
			}
			dir = parent
		}
	}

	all := []*build.Instance{}

	for _, p := range fp.pkgs {
		impPath, err := addImportQualifier(importPath(p.ImportPath), p.PkgName)
		p.ImportPath = string(impPath)
		if err != nil {
			p.ReportError(err)
		}

		all = append(all, p)
		rewriteFiles(p, cfg.ModuleRoot, false)
		if errs := fp.finalize(p); errs != nil {
			p.ReportError(errs)
			return all
		}

		l.addFiles(cfg.ModuleRoot, p)
		_ = p.Complete()
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].Dir < all[j].Dir
	})
	return all
}

// loadFunc creates a LoadFunc that can be used to create new build.Instances.
func (l *loader) loadFunc() build.LoadFunc {

	return func(pos token.Pos, path string) *build.Instance {
		cfg := l.cfg

		impPath := importPath(path)
		if isLocalImport(path) {
			return cfg.newErrInstance(pos, impPath,
				errors.Newf(pos, "relative import paths not allowed (%q)", path))
		}

		// is it a builtin?
		if strings.IndexByte(strings.Split(path, "/")[0], '.') == -1 {
			if l.cfg.StdRoot != "" {
				p := cfg.newInstance(pos, impPath)
				_ = l.importPkg(pos, p)
				return p
			}
			return nil
		}

		p := cfg.newInstance(pos, impPath)
		_ = l.importPkg(pos, p)
		return p
	}
}

func rewriteFiles(p *build.Instance, root string, isLocal bool) {
	p.Root = root

	normalizeFiles(p.BuildFiles)
	normalizeFiles(p.IgnoredFiles)
	normalizeFiles(p.OrphanedFiles)
	normalizeFiles(p.InvalidFiles)
	normalizeFiles(p.UnknownFiles)
}

func normalizeFiles(a []*build.File) {
	sort.Slice(a, func(i, j int) bool {
		return len(filepath.Dir(a[i].Filename)) < len(filepath.Dir(a[j].Filename))
	})
}

type fileProcessor struct {
	firstFile        string
	firstCommentFile string
	imported         map[string][]token.Pos
	allTags          map[string]bool
	allFiles         bool
	ignoreOther      bool // ignore files from other packages
	allPackages      bool

	c    *Config
	pkgs map[string]*build.Instance
	pkg  *build.Instance

	err errors.Error
}

func newFileProcessor(c *Config, p *build.Instance) *fileProcessor {
	return &fileProcessor{
		imported: make(map[string][]token.Pos),
		allTags:  make(map[string]bool),
		c:        c,
		pkgs:     map[string]*build.Instance{"_": p},
		pkg:      p,
	}
}

func countCUEFiles(c *Config, p *build.Instance) int {
	count := len(p.BuildFiles)
	for _, f := range p.IgnoredFiles {
		if c.Tools && strings.HasSuffix(f.Filename, "_tool.cue") {
			count++
		}
		if c.Tests && strings.HasSuffix(f.Filename, "_test.cue") {
			count++
		}
	}
	return count
}

func (fp *fileProcessor) finalize(p *build.Instance) errors.Error {
	if fp.err != nil {
		return fp.err
	}
	if countCUEFiles(fp.c, p) == 0 &&
		!fp.c.DataFiles &&
		(p.PkgName != "_" || !fp.allPackages) {
		fp.err = errors.Append(fp.err, &NoFilesError{Package: p, ignored: len(p.IgnoredFiles) > 0})
		return fp.err
	}

	for tag := range fp.allTags {
		p.AllTags = append(p.AllTags, tag)
	}
	sort.Strings(p.AllTags)

	p.ImportPaths, _ = cleanImports(fp.imported)

	return nil
}

func (fp *fileProcessor) add(pos token.Pos, root string, file *build.File, mode importMode) (added bool) {
	fullPath := file.Filename
	if fullPath != "-" {
		if !filepath.IsAbs(fullPath) {
			fullPath = filepath.Join(root, fullPath)
		}
	}
	file.Filename = fullPath

	base := filepath.Base(fullPath)

	// special * and _
	p := fp.pkg // default package

	// badFile := func(p *build.Instance, err errors.Error) bool {
	badFile := func(err errors.Error) bool {
		fp.err = errors.Append(fp.err, err)
		file.ExcludeReason = fp.err
		p.InvalidFiles = append(p.InvalidFiles, file)
		return true
	}

	match, data, err := matchFile(fp.c, file, true, fp.allFiles, fp.allTags)
	switch {
	case match:

	case err == nil:
		// Not a CUE file.
		p.OrphanedFiles = append(p.OrphanedFiles, file)
		return false

	case !errors.Is(err, errExclude):
		return badFile(err)

	default:
		file.ExcludeReason = err
		if file.Interpretation == "" {
			p.IgnoredFiles = append(p.IgnoredFiles, file)
		} else {
			p.OrphanedFiles = append(p.OrphanedFiles, file)
		}
		return false
	}

	pf, perr := parser.ParseFile(fullPath, data, parser.ImportsOnly, parser.ParseComments)
	if perr != nil {
		badFile(errors.Promote(perr, "add failed"))
		return true
	}

	_, pkg, pos := internal.PackageInfo(pf)
	if pkg == "" {
		pkg = "_"
	}

	switch {
	case pkg == p.PkgName, mode&allowAnonymous != 0:
	case fp.allPackages && pkg != "_":
		q := fp.pkgs[pkg]
		if q == nil {
			q = &build.Instance{
				PkgName: pkg,

				Dir:         p.Dir,
				DisplayPath: p.DisplayPath,
				ImportPath:  p.ImportPath + ":" + pkg,
				Root:        p.Root,
				Module:      p.Module,
			}
			fp.pkgs[pkg] = q
		}
		p = q

	case pkg != "_":

	default:
		file.ExcludeReason = excludeError{errors.Newf(pos, "no package name")}
		p.IgnoredFiles = append(p.IgnoredFiles, file)
		return false // don't mark as added
	}

	if !fp.c.AllCUEFiles {
		if err := shouldBuildFile(pf, fp); err != nil {
			if !errors.Is(err, errExclude) {
				fp.err = errors.Append(fp.err, err)
			}
			file.ExcludeReason = err
			p.IgnoredFiles = append(p.IgnoredFiles, file)
			return false
		}
	}

	if pkg != "" && pkg != "_" {
		if p.PkgName == "" {
			p.PkgName = pkg
			fp.firstFile = base
		} else if pkg != p.PkgName {
			if fp.ignoreOther {
				file.ExcludeReason = excludeError{errors.Newf(pos,
					"package is %s, want %s", pkg, p.PkgName)}
				p.IgnoredFiles = append(p.IgnoredFiles, file)
				return false
			}
			return badFile(&MultiplePackageError{
				Dir:      p.Dir,
				Packages: []string{p.PkgName, pkg},
				Files:    []string{fp.firstFile, base},
			})
		}
	}

	isTest := strings.HasSuffix(base, "_test"+cueSuffix)
	isTool := strings.HasSuffix(base, "_tool"+cueSuffix)

	if mode&importComment != 0 {
		qcom, line := findimportComment(data)
		if line != 0 {
			com, err := strconv.Unquote(qcom)
			if err != nil {
				badFile(errors.Newf(pos, "%s:%d: cannot parse import comment", fullPath, line))
			} else if p.ImportComment == "" {
				p.ImportComment = com
				fp.firstCommentFile = base
			} else if p.ImportComment != com {
				badFile(errors.Newf(pos, "found import comments %q (%s) and %q (%s) in %s", p.ImportComment, fp.firstCommentFile, com, base, p.Dir))
			}
		}
	}

	for _, decl := range pf.Decls {
		d, ok := decl.(*ast.ImportDecl)
		if !ok {
			continue
		}
		for _, spec := range d.Specs {
			quoted := spec.Path.Value
			path, err := strconv.Unquote(quoted)
			if err != nil {
				badFile(errors.Newf(
					spec.Path.Pos(),
					"%s: parser returned invalid quoted string: <%s>", fullPath, quoted,
				))
			}
			if !isTest || fp.c.Tests {
				fp.imported[path] = append(fp.imported[path], spec.Pos())
			}
		}
	}
	switch {
	case isTest:
		if fp.c.loader.cfg.Tests {
			p.BuildFiles = append(p.BuildFiles, file)
		} else {
			file.ExcludeReason = excludeError{errors.Newf(pos,
				"_test.cue files excluded in non-test mode")}
			p.IgnoredFiles = append(p.IgnoredFiles, file)
		}
	case isTool:
		if fp.c.loader.cfg.Tools {
			p.BuildFiles = append(p.BuildFiles, file)
		} else {
			file.ExcludeReason = excludeError{errors.Newf(pos,
				"_tool.cue files excluded in non-cmd mode")}
			p.IgnoredFiles = append(p.IgnoredFiles, file)
		}
	default:
		p.BuildFiles = append(p.BuildFiles, file)
	}
	return true
}

func findimportComment(data []byte) (s string, line int) {
	// expect keyword package
	word, data := parseWord(data)
	if string(word) != "package" {
		return "", 0
	}

	// expect package name
	_, data = parseWord(data)

	// now ready for import comment, a // comment
	// beginning and ending on the current line.
	for len(data) > 0 && (data[0] == ' ' || data[0] == '\t' || data[0] == '\r') {
		data = data[1:]
	}

	var comment []byte
	switch {
	case bytes.HasPrefix(data, slashSlash):
		i := bytes.Index(data, newline)
		if i < 0 {
			i = len(data)
		}
		comment = data[2:i]
	}
	comment = bytes.TrimSpace(comment)

	// split comment into `import`, `"pkg"`
	word, arg := parseWord(comment)
	if string(word) != "import" {
		return "", 0
	}

	line = 1 + bytes.Count(data[:cap(data)-cap(arg)], newline)
	return strings.TrimSpace(string(arg)), line
}

var (
	slashSlash = []byte("//")
	newline    = []byte("\n")
)

// skipSpaceOrComment returns data with any leading spaces or comments removed.
func skipSpaceOrComment(data []byte) []byte {
	for len(data) > 0 {
		switch data[0] {
		case ' ', '\t', '\r', '\n':
			data = data[1:]
			continue
		case '/':
			if bytes.HasPrefix(data, slashSlash) {
				i := bytes.Index(data, newline)
				if i < 0 {
					return nil
				}
				data = data[i+1:]
				continue
			}
		}
		break
	}
	return data
}

// parseWord skips any leading spaces or comments in data
// and then parses the beginning of data as an identifier or keyword,
// returning that word and what remains after the word.
func parseWord(data []byte) (word, rest []byte) {
	data = skipSpaceOrComment(data)

	// Parse past leading word characters.
	rest = data
	for {
		r, size := utf8.DecodeRune(rest)
		if unicode.IsLetter(r) || '0' <= r && r <= '9' || r == '_' {
			rest = rest[size:]
			continue
		}
		break
	}

	word = data[:len(data)-len(rest)]
	if len(word) == 0 {
		return nil, nil
	}

	return word, rest
}

func cleanImports(m map[string][]token.Pos) ([]string, map[string][]token.Pos) {
	all := make([]string, 0, len(m))
	for path := range m {
		all = append(all, path)
	}
	sort.Strings(all)
	return all, m
}

// isLocalImport reports whether the import path is
// a local import path, like ".", "..", "./foo", or "../foo".
func isLocalImport(path string) bool {
	return path == "." || path == ".." ||
		strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../")
}
