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

// Files in package are to a large extent based on Go files from the following
// Go packages:
//    - cmd/go/internal/load
//    - go/build

import (
	pathpkg "path"
	"path/filepath"
	"strings"
	"unicode"

	"cuelang.org/go/cue/ast"
	"cuelang.org/go/cue/build"
	"cuelang.org/go/cue/errors"
	"cuelang.org/go/cue/token"
	"cuelang.org/go/internal/encoding"
	"cuelang.org/go/internal/filetypes"

	// Trigger the unconditional loading of all core builtin packages if load
	// is used. This was deemed the simplest way to avoid having to import
	// this line explicitly, and thus breaking existing code, for the majority
	// of cases, while not introducing an import cycle.
	_ "cuelang.org/go/pkg"
)

// Instances returns the instances named by the command line arguments 'args'.
// If errors occur trying to load an instance it is returned with Incomplete
// set. Errors directly related to loading the instance are recorded in this
// instance, but errors that occur loading dependencies are recorded in these
// dependencies.
func Instances(args []string, c *Config) []*build.Instance {
	if c == nil {
		c = &Config{}
	}
	newC, err := c.complete()
	if err != nil {
		return []*build.Instance{c.newErrInstance(token.NoPos, "", err)}
	}
	c = newC

	l := c.loader

	// TODO: require packages to be placed before files. At some point this
	// could be relaxed.
	i := 0
	for ; i < len(args) && filetypes.IsPackage(args[i]); i++ {
	}

	a := []*build.Instance{}

	if len(args) == 0 || i > 0 {
		for _, m := range l.importPaths(args[:i]) {
			if m.Err != nil {
				inst := c.newErrInstance(token.NoPos, "", m.Err)
				a = append(a, inst)
				continue
			}
			a = append(a, m.Pkgs...)
		}
	}

	if args = args[i:]; len(args) > 0 {
		files, err := filetypes.ParseArgs(args)
		if err != nil {
			return []*build.Instance{c.newErrInstance(token.NoPos, "", err)}
		}
		a = append(a, l.cueFilesPackage(files))
	}

	for _, p := range a {
		tags, err := findTags(p)
		if err != nil {
			p.ReportError(err)
		}
		l.tags = append(l.tags, tags...)
	}

	// TODO(api): have API call that returns an error which is the aggregate
	// of all build errors. Certain errors, like these, hold across builds.
	if err := injectTags(c.Tags, l); err != nil {
		for _, p := range a {
			p.ReportError(err)
		}
		return a
	}

	if l.replacements == nil {
		return a
	}

	for _, p := range a {
		for _, f := range p.Files {
			ast.Walk(f, nil, func(n ast.Node) {
				if ident, ok := n.(*ast.Ident); ok {
					if v, ok := l.replacements[ident.Node]; ok {
						ident.Node = v
					}
				}
			})
		}
	}

	return a
}

// Mode flags for loadImport and download (in get.go).
const (
	// resolveImport means that loadImport should do import path expansion.
	// That is, resolveImport means that the import path came from
	// a source file and has not been expanded yet to account for
	// vendoring or possible module adjustment.
	// Every import path should be loaded initially with resolveImport,
	// and then the expanded version (for example with the /vendor/ in it)
	// gets recorded as the canonical import path. At that point, future loads
	// of that package must not pass resolveImport, because
	// disallowVendor will reject direct use of paths containing /vendor/.
	resolveImport = 1 << iota
)

type loader struct {
	cfg          *Config
	stk          importStack
	tags         []*tag // tags found in files
	buildTags    map[string]bool
	replacements map[ast.Node]ast.Node
}

func (l *loader) abs(filename string) string {
	if !isLocalImport(filename) {
		return filename
	}
	return filepath.Join(l.cfg.Dir, filename)
}

// cueFilesPackage creates a package for building a collection of CUE files
// (typically named on the command line).
func (l *loader) cueFilesPackage(files []*build.File) *build.Instance {
	pos := token.NoPos
	cfg := l.cfg
	cfg.filesMode = true
	// ModInit() // TODO: support modules
	pkg := l.cfg.Context.NewInstance(cfg.Dir, l.loadFunc())

	_, err := filepath.Abs(cfg.Dir)
	if err != nil {
		return cfg.newErrInstance(pos, toImportPath(cfg.Dir),
			errors.Wrapf(err, pos, "could not convert '%s' to absolute path", cfg.Dir))
	}

	for _, bf := range files {
		f := bf.Filename
		if f == "-" {
			continue
		}
		if !filepath.IsAbs(f) {
			f = filepath.Join(cfg.Dir, f)
		}
		fi, err := cfg.fileSystem.stat(f)
		if err != nil {
			return cfg.newErrInstance(pos, toImportPath(f),
				errors.Wrapf(err, pos, "could not find file"))
		}
		if fi.IsDir() {
			return cfg.newErrInstance(token.NoPos, toImportPath(f),
				errors.Newf(pos, "file is a directory %v", f))
		}
	}

	fp := newFileProcessor(cfg, pkg)
	for _, file := range files {
		fp.add(pos, cfg.Dir, file, allowAnonymous)
	}

	// TODO: ModImportFromFiles(files)
	pkg.Dir = cfg.Dir
	rewriteFiles(pkg, pkg.Dir, true)
	for _, err := range errors.Errors(fp.finalize(pkg)) { // ImportDir(&ctxt, dir, 0)
		var x *NoFilesError
		if len(pkg.OrphanedFiles) == 0 || !errors.As(err, &x) {
			pkg.ReportError(err)
		}
	}
	// TODO: Support module importing.
	// if ModDirImportPath != nil {
	// 	// Use the effective import path of the directory
	// 	// for deciding visibility during pkg.load.
	// 	bp.ImportPath = ModDirImportPath(dir)
	// }

	l.addFiles(cfg.Dir, pkg)

	pkg.User = true
	l.stk.Push("user")
	_ = pkg.Complete()
	l.stk.Pop()
	pkg.User = true
	//pkg.LocalPrefix = dirToImportPath(dir)
	pkg.DisplayPath = "command-line-arguments"

	return pkg
}

func (l *loader) addFiles(dir string, p *build.Instance) {
	for _, f := range p.BuildFiles {
		d := encoding.NewDecoder(f, &encoding.Config{
			Stdin:     l.cfg.stdin(),
			ParseFile: l.cfg.ParseFile,
		})
		for ; !d.Done(); d.Next() {
			_ = p.AddSyntax(d.File())
		}
		if err := d.Err(); err != nil {
			p.ReportError(errors.Promote(err, "load"))
		}
		d.Close()
	}
}

func cleanImport(path string) string {
	orig := path
	path = pathpkg.Clean(path)
	if strings.HasPrefix(orig, "./") && path != ".." && !strings.HasPrefix(path, "../") {
		path = "./" + path
	}
	return path
}

// An importStack is a stack of import paths, possibly with the suffix " (test)" appended.
// The import path of a test package is the import path of the corresponding
// non-test package with the suffix "_test" added.
type importStack []string

func (s *importStack) Push(p string) {
	*s = append(*s, p)
}

func (s *importStack) Pop() {
	*s = (*s)[0 : len(*s)-1]
}

func (s *importStack) Copy() []string {
	return append([]string{}, *s...)
}

// shorterThan reports whether sp is shorter than t.
// We use this to record the shortest import sequences
// that leads to a particular package.
func (sp *importStack) shorterThan(t []string) bool {
	s := *sp
	if len(s) != len(t) {
		return len(s) < len(t)
	}
	// If they are the same length, settle ties using string ordering.
	for i := range s {
		if s[i] != t[i] {
			return s[i] < t[i]
		}
	}
	return false // they are equal
}

// reusePackage reuses package p to satisfy the import at the top
// of the import stack stk. If this use causes an import loop,
// reusePackage updates p's error information to record the loop.
func (l *loader) reusePackage(p *build.Instance) *build.Instance {
	// We use p.Internal.Imports==nil to detect a package that
	// is in the midst of its own loadPackage call
	// (all the recursion below happens before p.Internal.Imports gets set).
	if p.ImportPaths == nil {
		if err := lastError(p); err == nil {
			err = l.errPkgf(nil, "import cycle not allowed")
			err.IsImportCycle = true
			report(p, err)
		}
		p.Incomplete = true
	}
	// Don't rewrite the import stack in the error if we have an import cycle.
	// If we do, we'll lose the path that describes the cycle.
	if err := lastError(p); err != nil && !err.IsImportCycle && l.stk.shorterThan(err.ImportStack) {
		err.ImportStack = l.stk.Copy()
	}
	return p
}

// dirToImportPath returns the pseudo-import path we use for a package
// outside the CUE path. It begins with _/ and then contains the full path
// to the directory. If the package lives in c:\home\gopher\my\pkg then
// the pseudo-import path is _/c_/home/gopher/my/pkg.
// Using a pseudo-import path like this makes the ./ imports no longer
// a special case, so that all the code to deal with ordinary imports works
// automatically.
func dirToImportPath(dir string) string {
	return pathpkg.Join("_", strings.Map(makeImportValid, filepath.ToSlash(dir)))
}

func makeImportValid(r rune) rune {
	// Should match Go spec, compilers, and ../../go/parser/parser.go:/isValidImport.
	const illegalChars = `!"#$%&'()*,:;<=>?[\]^{|}` + "`\uFFFD"
	if !unicode.IsGraphic(r) || unicode.IsSpace(r) || strings.ContainsRune(illegalChars, r) {
		return '_'
	}
	return r
}
