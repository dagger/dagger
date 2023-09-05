package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/core/resourceid"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/opencontainers/go-digest"
)

type Function struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Args        []*FunctionArg `json:"functionArgs"`
	ReturnType  *TypeDef       `json:"returnType"`
	IsStatic    bool           `json:"isStatic"`

	// (Not in public API) Used to invoke function in the context of its module.
	ModuleID ModuleID `json:"moduleID,omitempty"`
}

func (fn *Function) ID() (FunctionID, error) {
	return resourceid.Encode(fn)
}

func (fn *Function) Digest() (digest.Digest, error) {
	// TODO: does this need to unpack ModuleID and stable digest that?
	return stableDigest(fn)
}

func (fn Function) Clone() (*Function, error) {
	cp := fn
	cp.Args = make([]*FunctionArg, len(fn.Args))
	var err error
	for i, arg := range fn.Args {
		cp.Args[i], err = arg.Clone()
		if err != nil {
			return nil, fmt.Errorf("failed to clone function arg %q: %w", arg.Name, err)
		}
	}
	cp.ReturnType, err = fn.ReturnType.Clone()
	if err != nil {
		return nil, fmt.Errorf("failed to clone return type: %w", err)
	}
	return &cp, nil
}

func (fn *Function) Call(
	ctx context.Context,
	bk *buildkit.Client,
	progSock string,
	modCache *ModuleCache,
	installDeps InstallDepsCallback,
	parent any,
	input map[string]any,
) (any, error) {
	// TODO: if return type non-null, assert on that here
	// TODO: handle setting default values, they won't be set when going through "dynamic call" codepath

	if fn.ModuleID == "" {
		return nil, fmt.Errorf("invalid function with unset module %q", fn.Name)
	}

	mod, err := fn.ModuleID.Decode()
	if err != nil {
		return nil, fmt.Errorf("failed to decode module for function %q: %w", fn.Name, err)
	}

	// TODO: re-incorporate support for caching certain exit codes (maybe simplify a bit too)
	resource, outputBytes, _, err := mod.execModule(ctx, bk, progSock, modCache, installDeps, check.Name, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to exec check module container: %w", err)
	}
}

type FunctionArg struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	TypeDef      *TypeDef `json:"typeDef"`
	DefaultValue any      `json:"defaultValue"`
}

func (arg FunctionArg) Clone() (*FunctionArg, error) {
	cp := arg
	var err error
	cp.TypeDef, err = arg.TypeDef.Clone()
	if err != nil {
		return nil, fmt.Errorf("failed to clone type def: %w", err)
	}

	// TODO: not sure there's any better way to clone any besides a ser/deser cycle
	bs, err := json.Marshal(arg.DefaultValue)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal default value: %w", err)
	}
	if err := json.Unmarshal(bs, &cp.DefaultValue); err != nil {
		return nil, fmt.Errorf("failed to unmarshal default value: %w", err)
	}

	return &cp, nil
}

type TypeDef struct {
	Kind TypeDefKind `json:"kind"`

	// only valid for kind NON_NULL and LIST
	ElementType *TypeDef `json:"elementType"`

	// only valid for kind OBJECT
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Fields      []*Function `json:"fields"`
}

func (typeDef TypeDef) Clone() (*TypeDef, error) {
	cp := typeDef
	if typeDef.ElementType != nil {
		var err error
		cp.ElementType, err = typeDef.ElementType.Clone()
		if err != nil {
			return nil, fmt.Errorf("failed to clone element type: %w", err)
		}
	}
	cp.Fields = make([]*Function, len(typeDef.Fields))
	for i, fn := range typeDef.Fields {
		var err error
		cp.Fields[i], err = fn.Clone()
		if err != nil {
			return nil, fmt.Errorf("failed to clone function %q: %w", fn.Name, err)
		}
	}
	return &cp, nil
}

func (typeDef TypeDef) FieldByName(name string) (*Function, bool) {
	for _, fn := range typeDef.Fields {
		if fn.Name == name {
			return fn, true
		}
	}
	return nil, false
}

type TypeDefKind string

func (k TypeDefKind) String() string {
	return string(k)
}

const (
	TypeDefKindString  TypeDefKind = "String"
	TypeDefKindInteger TypeDefKind = "Integer"
	TypeDefKindBoolean TypeDefKind = "Boolean"
	TypeDefKindNonNull TypeDefKind = "NonNull"
	TypeDefKindList    TypeDefKind = "List"
	TypeDefKindObject  TypeDefKind = "Object"
)

type FunctionCall struct {
	Name string `json:"name"`
}

func (fnCall *FunctionCall) ReturnValue() error {
}

func (fnCall *FunctionCall) ReturnValue() error {
	// TODO: error out if not coming from a mod

	// TODO: doc, a bit silly looking but actually works out nicely
	return bk.IOReaderExport(ctx, bytes.NewReader([]byte(valStr)), filepath.Join(modMetaDirPath, modMetaOutputPath), 0600)
}

type FunctionInput struct {
	// The name of the entrypoint to invoke. If unset, then the module
	// definition should be returned.
	Name string `json:"name"`

	// The arguments to pass to the entrypoint, serialized as json. The json
	// object maps argument names to argument values.
	Args string `json:"args"`
}

func (mod *Module) FunctionInput(ctx context.Context, bk *buildkit.Client) (*FunctionInput, error) {
	// TODO: error out if not coming from an mod

	// TODO: doc, a bit silly looking but actually works out nicely
	inputBytes, err := bk.ReadCallerHostFile(ctx, filepath.Join(modMetaDirPath, modMetaInputPath))
	if err != nil {
		return nil, fmt.Errorf("failed to read entrypoint input file: %w", err)
	}
	var input FunctionInput
	if err := json.Unmarshal(inputBytes, &input); err != nil {
		return nil, fmt.Errorf("failed to unmarshal entrypoint input: %w", err)
	}
	return &input, nil
}
