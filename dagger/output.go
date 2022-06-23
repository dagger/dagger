package dagger

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
)

// TODO: use generics?
type DaggerType string

const (
	DaggerTypeFS     = "fs"
	DaggerTypeString = "string"
	DaggerTypeStruct = "struct"
)

type Result struct {
	daggerType DaggerType
	opcodes    string // NOTE: []byte would be a better fit but it makes this struct not comparable
	selector   string
	// TODO: support for optional concrete val
	// value      string
}

func (o Result) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		DaggerType DaggerType `json:"daggerType"`
		Opcodes    string     `json:"opcodes"`
		Selector   string     `json:"selector"`
	}{
		DaggerType: o.daggerType,
		Opcodes:    o.opcodes,
		Selector:   o.selector,
	})
}

func (o *Result) UnmarshalJSON(b []byte) error {
	var v struct {
		DaggerType DaggerType `json:"daggerType"`
		Opcodes    string     `json:"opcodes"`
		Selector   string     `json:"selector"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*o = Result{
		daggerType: v.DaggerType,
		opcodes:    v.Opcodes,
		selector:   v.Selector,
	}
	return nil
}

func (o Result) GetField(field string) Result {
	if o.daggerType != DaggerTypeStruct {
		panic(fmt.Sprintf("GetField called on non-struct result: %v", o))
	}
	return Result{
		daggerType: o.daggerType,
		opcodes:    o.opcodes,
		selector:   field,
	}
}

func (o Result) FS() FS {
	return FS{res: o}
}

func (o Result) String() String {
	return String{res: o}
}

// resolve returns the final result after following any selectors
func (o Result) resolve(ctx *Context) (Result, error) {
	if o.daggerType != DaggerTypeStruct {
		return o, nil
	}

	var def pb.Definition
	if err := json.Unmarshal([]byte(o.opcodes), &def); err != nil {
		return Result{}, err
	}
	res, err := ctx.client.Solve(ctx.ctx, bkgw.SolveRequest{
		Definition: &def,
	})
	if err != nil {
		return Result{}, err
	}
	ref, err := res.SingleRef()
	if err != nil {
		return Result{}, err
	}
	bytes, err := ref.ReadFile(ctx.ctx, bkgw.ReadRequest{
		Filename: "/dagger.json",
	})
	if err != nil {
		return Result{}, fmt.Errorf("resolve failed to read /dagger.json: %w", err)
	}

	if o.selector != "" {
		var blob interface{}
		// if err := json.Unmarshal(subbytes, &blob); err != nil {
		if err := json.Unmarshal(bytes, &blob); err != nil {
			return Result{}, err
		}
		m, ok := blob.(map[string]interface{})
		if !ok {
			return Result{}, fmt.Errorf("not a json struct: %s", string(bytes))
		}
		rawSubfield, ok := m[o.selector]
		if !ok {
			return Result{}, fmt.Errorf("no field %q in %s (%s)", o.selector, string(bytes), o.selector)
		}
		subfield, ok := rawSubfield.(map[string]interface{})
		if !ok {
			return Result{}, fmt.Errorf("field %q is not a struct in %s", o.selector, string(bytes))
		}
		rawOpcodes, ok := subfield["opcodes"]
		if !ok {
			return Result{}, fmt.Errorf("opcodes not found under field %q in %s", o.selector, string(bytes))
		}
		o.opcodes, ok = rawOpcodes.(string)
		if !ok {
			return Result{}, fmt.Errorf("opcodes not a string under field %q in %s", o.selector, string(bytes))
		}
		rawType, ok := subfield["daggerType"]
		if !ok {
			return Result{}, fmt.Errorf("daggerType not found under field %q in %s", o.selector, string(bytes))
		}
		o.daggerType = DaggerType(rawType.(string))
		rawSelector, ok := subfield["selector"]
		if !ok {
			return Result{}, fmt.Errorf("selector not found under field %q in %s", o.selector, string(bytes))
		}
		o.selector = rawSelector.(string)
		o, err = o.resolve(ctx)
		if err != nil {
			return Result{}, fmt.Errorf("subresolve failed: %w", err)
		}
	}

	return o, nil
}

type FS struct {
	res   Result
	bkRef bkgw.Reference // optional cached reference, set if FS has been evaluated
}

func (fs FS) MarshalJSON() ([]byte, error) {
	return json.Marshal(fs.res)
}

func (fs *FS) UnmarshalJSON(b []byte) error {
	var res Result
	if err := json.Unmarshal(b, &res); err != nil {
		return err
	}
	fs.res = res
	return nil
}

func (fs *FS) Evaluate(ctx *Context) {
	// TODO: synchronization
	if fs.bkRef != nil {
		return
	}
	res, err := fs.res.resolve(ctx)
	if err != nil {
		panic(err) // TODO?
	}
	fs.res = res
	var def pb.Definition
	if err := json.Unmarshal([]byte(fs.res.opcodes), &def); err != nil {
		panic(err)
	}
	bkRes, err := ctx.client.Solve(ctx.ctx, bkgw.SolveRequest{
		Evaluate:   true,
		Definition: &def,
	})
	if err != nil {
		panic(err)
	}
	fs.bkRef, err = bkRes.SingleRef()
	if err != nil {
		panic(err)
	}
}

func (fs *FS) ReadFile(ctx *Context, path string) ([]byte, error) {
	fs.Evaluate(ctx)
	return fs.bkRef.ReadFile(ctx.ctx, bkgw.ReadRequest{Filename: path})
}

// TODO: don't love this being public, but it's needed by core actions for now
func (fs *FS) Definition(ctx *Context) *pb.Definition {
	fs.Evaluate(ctx)
	var def pb.Definition
	if err := json.Unmarshal([]byte(fs.res.opcodes), &def); err != nil {
		panic(err)
	}
	return &def
}

// TODO: don't love this being public, but it's needed by core actions for now
func NewFS(ctx *Context, def *pb.Definition) (*FS, error) {
	bytes, err := json.Marshal(def)
	if err != nil {
		return nil, err
	}
	return &FS{res: Result{
		daggerType: DaggerTypeFS,
		opcodes:    string(bytes),
	}}, nil
}

type String struct {
	res   Result
	bkRef bkgw.Reference // optional cached reference, set if result has been evaluated
}

func (s String) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.res)
}

func (s *String) UnmarshalJSON(b []byte) error {
	var res Result
	if err := json.Unmarshal(b, &res); err != nil {
		return err
	}
	s.res = res
	return nil
}

func (s *String) Evaluate(ctx *Context) string {
	// TODO: synchronization
	if s.bkRef == nil {
		res, err := s.res.resolve(ctx)
		if err != nil {
			panic(err) // TODO?
		}
		s.res = res
		var def pb.Definition
		if err := json.Unmarshal([]byte(s.res.opcodes), &def); err != nil {
			panic(err)
		}
		bkRes, err := ctx.client.Solve(ctx.ctx, bkgw.SolveRequest{
			Evaluate:   true,
			Definition: &def,
		})
		if err != nil {
			panic(err)
		}
		s.bkRef, err = bkRes.SingleRef()
		if err != nil {
			panic(err)
		}
	}
	bytes, err := s.bkRef.ReadFile(ctx.ctx, bkgw.ReadRequest{Filename: "/value"})
	if err != nil {
		panic(err)
	}
	return string(bytes)
}

func ToString(in string) String {
	llbdef, err := llb.Scratch().File(llb.Mkfile("/value", 0644, []byte(in))).Marshal(context.TODO())
	if err != nil {
		panic(err) // TODO?
	}
	bytes, err := json.Marshal(llbdef.ToPB())
	if err != nil {
		panic(err)
	}
	return String{res: Result{
		daggerType: DaggerTypeString,
		opcodes:    string(bytes),
	}}
}

func ToStrings(strings ...string) Strings {
	var out []String
	for _, s := range strings {
		out = append(out, ToString(s))
	}
	return out
}

type Strings []String

func (strings Strings) Add(s String) Strings {
	return append(strings, s)
}

func (strings Strings) Evaluate(ctx *Context) []string {
	var out []string
	for _, s := range strings {
		out = append(out, s.Evaluate(ctx))
	}
	return out
}
