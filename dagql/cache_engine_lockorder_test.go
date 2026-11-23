package dagql

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The Cloud cache's locks are taken in one order: egraphMu, then an engine
// cache's lock (remoteCacheState.mu), then an entry's holder list's
// (entryHolders.mu), the leaf. These tests read the package's functions, by
// name, as the service's lock-order tests read its own: a call is followed
// to every function of the package with its name, whatever the receiver, so
// a call they follow may reach less than they find. They don't follow calls
// through function values or interfaces.

// engineLockTakers are the calls that take an engine cache's lock.
var engineLockTakers = []string{"rlockEngineLocked", "lockEngine"}

// engineLockNesting are the functions that take an engine cache's lock and
// call, by name, another that takes one, and why that is safe: each lets
// go of its own first. A function that holds one engine cache's lock never
// waits for another's: two readers, or a reader and a writer of each, would
// wait for each other.
var engineLockNesting = map[string]string{
	"RemoteEntryInfo": "lets go of the holding's engine cache's lock before it describes the entry's holdings (remoteEntryInfoLocked)",
	"HolderClosure":   "its walks in pieces (holderClosureInPieces) let go of theirs at done, before its last walk takes it in one hold",
}

// Every function that takes an engine cache's lock calls none that takes
// another while it holds its own (engineLockNesting).
func TestEngineCacheLocksDontNest(t *testing.T) {
	t.Parallel()
	bodies := dagqlBodies(t).bodies
	takers := functionsCalling(bodies, engineLockTakers...)
	var found []string
	for _, name := range takers {
		for _, reached := range reachedFrom(bodies, name, engineLockTakers) {
			if reached != name && slices.Contains(takers, reached) {
				found = append(found, name)
				break
			}
		}
	}
	slices.Sort(found)
	var listed []string
	for name := range engineLockNesting {
		listed = append(listed, name)
	}
	slices.Sort(listed)
	require.Equal(t, listed, found, "the functions holding an engine cache's lock that reach another's")
}

// An engine cache's lock is only ever taken under egraphMu: each function
// that takes it either takes egraphMu for reading itself first, or is a
// helper whose name says it requires egraphMu (…Locked), or is the read in
// pieces, which holds egraphMu with each piece.
func TestEngineCacheLocksAreTakenUnderTheCachesLock(t *testing.T) {
	t.Parallel()
	funcs := dagqlBodies(t)
	for _, name := range functionsCalling(funcs.bodies, engineLockTakers...) {
		if strings.HasSuffix(name, "Locked") || name == "lockEngine" {
			continue
		}
		takes := false
		for _, fn := range funcs.bodies[name] {
			src := funcs.source(fn)
			takes = takes || strings.Contains(src, "egraphMu.RLock()") || strings.Contains(src, "readInPieces()") || strings.Contains(src, "storedSizesRLock()")
		}
		require.True(t, takes, "%s takes an engine cache's lock without egraphMu", name)
	}
}

// An entry's holder list's lock is a leaf: what holds it calls nothing
// that takes egraphMu, an engine cache's lock, or another holder list's.
func TestEntryHolderListLockIsALeaf(t *testing.T) {
	t.Parallel()
	funcs := dagqlBodies(t)
	holders := []string{"lockHoldersLocked", "putHolderLocked", "deleteHolderLocked", "holderRefsLocked"}
	for _, name := range holders {
		require.NotEmpty(t, funcs.bodies[name], "%s is in the package", name)
		for _, reached := range reachedFrom(funcs.bodies, name, nil) {
			if reached == name || slices.Contains(holders, reached) {
				continue
			}
			for _, fn := range funcs.bodies[reached] {
				src := funcs.source(fn)
				require.False(t, strings.Contains(src, "egraphMu.") || strings.Contains(src, "rlockEngineLocked(") || strings.Contains(src, ".mu.Lock()") || strings.Contains(src, ".mu.RLock()"),
					"%s, under an entry's holder list's lock, reaches %s, which takes a lock", name, reached)
			}
		}
	}
}

// dagqlFuncs are the package's own functions, not its tests', by name:
// every receiver's of that name, with their source.
type dagqlFuncs struct {
	bodies map[string][]*ast.FuncDecl
	fset   *token.FileSet
	files  map[string][]byte
}

// dagqlBodies parses the package's own files, not its tests.
func dagqlBodies(t *testing.T) *dagqlFuncs {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	require.NoError(t, err)
	funcs := &dagqlFuncs{bodies: map[string][]*ast.FuncDecl{}, fset: token.NewFileSet(), files: map[string][]byte{}}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		require.NoError(t, err)
		funcs.files[path] = src
		file, err := parser.ParseFile(funcs.fset, path, src, 0)
		require.NoError(t, err)
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
				funcs.bodies[fn.Name.Name] = append(funcs.bodies[fn.Name.Name], fn)
			}
		}
	}
	return funcs
}

// source returns fn's source text.
func (funcs *dagqlFuncs) source(fn *ast.FuncDecl) string {
	start, end := funcs.fset.Position(fn.Pos()), funcs.fset.Position(fn.End())
	return string(funcs.files[start.Filename][start.Offset:end.Offset])
}

// calledNames are the names fn calls: a plain function's, or a method's or
// another package's function's last name.
func calledNames(fn *ast.FuncDecl) []string {
	var names []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch f := call.Fun.(type) {
		case *ast.Ident:
			names = append(names, f.Name)
		case *ast.SelectorExpr:
			names = append(names, f.Sel.Name)
		}
		return true
	})
	return names
}

// functionsCalling are the package's functions that call any of names,
// sorted.
func functionsCalling(bodies map[string][]*ast.FuncDecl, names ...string) []string {
	var out []string
	for name, fns := range bodies {
		for _, fn := range fns {
			if slices.ContainsFunc(calledNames(fn), func(n string) bool { return slices.Contains(names, n) }) {
				out = append(out, name)
				break
			}
		}
	}
	slices.Sort(out)
	return out
}

// reachedFrom are the package's functions name reaches through calls, by
// name, itself included, not following calls to the names in skip.
func reachedFrom(bodies map[string][]*ast.FuncDecl, name string, skip []string) []string {
	seen := map[string]bool{name: true}
	queue := []string{name}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		for _, fn := range bodies[next] {
			for _, called := range calledNames(fn) {
				if seen[called] || slices.Contains(skip, called) || bodies[called] == nil {
					continue
				}
				seen[called] = true
				queue = append(queue, called)
			}
		}
	}
	var out []string
	for n := range seen {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}
