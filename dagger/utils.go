package dagger

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"cuelang.org/go/cue"
	cueAst "cuelang.org/go/cue/ast"
	cueerrors "cuelang.org/go/cue/errors"
	cueformat "cuelang.org/go/cue/format"
	cueload "cuelang.org/go/cue/load"
	cueParser "cuelang.org/go/cue/parser"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/imagemetaresolver"
	"github.com/pkg/errors"
)

// A nil equivalent for cue.Value (when returning errors)
var qnil cue.Value

type Fillable interface {
	Fill(interface{}) error
}

func Discard() Fillable {
	return discard{}
}

type discard struct{}

func (d discard) Fill(x interface{}) error {
	return nil
}

type fillableValue struct {
	root cue.Value
}

func cuePrint(v cue.Value) (string, error) {
	b, err := cueformat.Node(v.Syntax())
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (f *fillableValue) Fill(v interface{}) error {
	root2 := f.root.Fill(v)
	if err := root2.Err(); err != nil {
		return err
	}
	f.root = root2
	return nil
}

func cueScratch(r *cue.Runtime) Fillable {
	f := &fillableValue{}
	if inst, err := r.Compile("", ""); err == nil {
		f.root = inst.Value()
	}
	return f
}

func cueErr(err error) error {
	return fmt.Errorf("%s", cueerrors.Details(err, &cueerrors.Config{}))
}

func cueDecodeArray(a cue.Value, idx int, out interface{}) {
	a.LookupPath(cue.MakePath(cue.Index(idx))).Decode(out)
}

func cueToJSON(v cue.Value) JSON {
	var out JSON
	v.Walk(
		func(v cue.Value) bool {
			b, err := v.MarshalJSON()
			if err == nil {
				newOut, err := out.Set(b, cuePathToStrings(v.Path())...)
				if err == nil {
					out = newOut
				}
				return false
			}
			return true
		},
		nil,
	)
	return out
}

// Build a cue instance from a directory and args
func cueBuild(r *cue.Runtime, cueRoot string, buildArgs ...string) (*cue.Instance, error) {
	var err error
	cueRoot, err = filepath.Abs(cueRoot)
	if err != nil {
		return nil, err
	}
	buildConfig := &cueload.Config{
		ModuleRoot: cueRoot,
		Dir:        cueRoot,
	}
	instances := cueload.Instances(buildArgs, buildConfig)
	if len(instances) != 1 {
		return nil, errors.New("only one package is supported at a time")
	}
	return r.Build(instances[0])
}

func debugJSON(v interface{}) {
	if os.Getenv("DEBUG") != "" {
		e := json.NewEncoder(os.Stderr)
		e.SetIndent("", "  ")
		e.Encode(v)
	}
}

func debugf(msg string, args ...interface{}) {
	if !strings.HasSuffix(msg, "\n") {
		msg = msg + "\n"
	}
	if os.Getenv("DEBUG") != "" {
		fmt.Fprintf(os.Stderr, msg, args...)
	}
}

func debug(msg string) {
	if os.Getenv("DEBUG") != "" {
		fmt.Fprintln(os.Stderr, msg)
	}
}

func randomID(size int) (string, error) {
	b := make([]byte, size)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}

func cueWrapExpr(p string, v cueAst.Expr) (cueAst.Expr, error) {
	pExpr, err := cueParser.ParseExpr("path", p)
	if err != nil {
		return v, err
	}
	out := v
	cursor := pExpr
walk:
	for {
		switch c := cursor.(type) {
		case *cueAst.SelectorExpr:
			out = cueAst.NewStruct(
				&cueAst.Field{
					Value: out,
					Label: c.Sel,
				},
			)
			cursor = c.X
		case *cueAst.Ident:
			out = cueAst.NewStruct(
				&cueAst.Field{
					Value: out,
					Label: c,
				},
			)
			break walk
		default:
			return out, fmt.Errorf("invalid path expression: %q", p)
		}
	}
	return out, nil
}

func cueWrapFile(p string, v interface{}) (*cueAst.File, error) {
	f, err := cueParser.ParseFile("value", v)
	if err != nil {
		return f, err
	}
	decls := make([]cueAst.Decl, 0, len(f.Decls))
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *cueAst.Field:
			wrappedExpr, err := cueWrapExpr(p, cueAst.NewStruct(d))
			if err != nil {
				return f, err
			}
			decls = append(decls, &cueAst.EmbedDecl{Expr: wrappedExpr})
		case *cueAst.EmbedDecl:
			wrappedExpr, err := cueWrapExpr(p, d.Expr)
			if err != nil {
				return f, err
			}
			d.Expr = wrappedExpr
			decls = append(decls, d)
		case *cueAst.ImportDecl:
			decls = append(decls, decl)
		default:
			fmt.Printf("skipping unsupported decl type %#v\n\n", decl)
			continue
		}
	}
	f.Decls = decls
	return f, nil
}

func cueIsEmptyStruct(v cue.Value) bool {
	if st, err := v.Struct(); err == nil {
		if st.Len() == 0 {
			return true
		}
	}
	return false
}

// Return false if v is not concrete, or contains any
// non-concrete fields or items.
func cueIsConcrete(v cue.Value) bool {
	// FIXME: use Value.Walk?
	if it, err := v.Fields(); err == nil {
		for it.Next() {
			if !cueIsConcrete(it.Value()) {
				return false
			}
		}
		return true
	}
	if it, err := v.List(); err == nil {
		for it.Next() {
			if !cueIsConcrete(it.Value()) {
				return false
			}
		}
		return true
	}
	dv, _ := v.Default()
	return v.IsConcrete() || dv.IsConcrete()
}

// LLB Helper to pull a Docker image + all its metadata
func llbDockerImage(ref string) llb.State {
	return llb.Image(
		ref,
		llb.WithMetaResolver(imagemetaresolver.Default()),
	)
}

func cueStringsToCuePath(parts ...string) cue.Path {
	selectors := make([]cue.Selector, 0, len(parts))
	for _, part := range parts {
		selectors = append(selectors, cue.Str(part))
	}
	return cue.MakePath(selectors...)
}

func cuePathToStrings(p cue.Path) []string {
	selectors := p.Selectors()
	out := make([]string, len(selectors))
	for i, sel := range selectors {
		out[i] = sel.String()
	}
	return out
}

// Validate a cue path, and return a canonical version
func cueCleanPath(p string) (string, error) {
	cp := cue.ParsePath(p)
	return cp.String(), cp.Err()
}

func autoMarshal(value interface{}) ([]byte, error) {
	switch v := value.(type) {
	case []byte:
		return v, nil
	case string:
		return []byte(v), nil
	case io.Reader:
		return ioutil.ReadAll(v)
	default:
		return nil, fmt.Errorf("unsupported marshal inoput type")
	}
	return []byte(fmt.Sprintf("%v", value)), nil
}
