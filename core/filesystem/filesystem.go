package filesystem

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/moby/buildkit/client/llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

type FSID string

type Filesystem struct {
	ID FSID `json:"id"`
}

func (f *Filesystem) ToDefinition() (*pb.Definition, error) {
	jsonBytes := make([]byte, base64.StdEncoding.DecodedLen(len(f.ID)))
	n, err := base64.StdEncoding.Decode(jsonBytes, []byte(f.ID))
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal fs bytes: %v", err)
	}
	jsonBytes = jsonBytes[:n]
	var result pb.Definition
	if err := json.Unmarshal(jsonBytes, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal result: %v: %s", err, string(jsonBytes))
	}
	return &result, nil
}

func (f *Filesystem) ToState() (llb.State, error) {
	def, err := f.ToDefinition()
	if err != nil {
		return llb.State{}, nil
	}
	defop, err := llb.NewDefinitionOp(def)
	if err != nil {
		return llb.State{}, err
	}
	return llb.NewState(defop), nil
}

func (f *Filesystem) ReadFile(ctx context.Context, gw bkgw.Client, path string) ([]byte, error) {
	def, err := f.ToDefinition()
	if err != nil {
		return nil, err
	}

	res, err := gw.Solve(ctx, bkgw.SolveRequest{
		Definition: def,
	})
	if err != nil {
		return nil, err
	}
	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}
	outputBytes, err := ref.ReadFile(ctx, bkgw.ReadRequest{
		Filename: path,
	})
	if err != nil {
		return nil, err
	}
	return outputBytes, nil
}

func New(id FSID) *Filesystem {
	return &Filesystem{
		ID: id,
	}
}

func FromDefinition(def *llb.Definition) *Filesystem {
	jsonBytes, err := json.Marshal(def.ToPB())
	if err != nil {
		panic(err)
	}
	b64Bytes := make([]byte, base64.StdEncoding.EncodedLen(len(jsonBytes)))
	base64.StdEncoding.Encode(b64Bytes, jsonBytes)
	return &Filesystem{
		ID: FSID(b64Bytes),
	}
}

func FromState(ctx context.Context, st llb.State, platform specs.Platform) (*Filesystem, error) {
	def, err := st.Marshal(ctx, llb.Platform(platform))
	if err != nil {
		return nil, err
	}
	return FromDefinition(def), nil
}

func FromSource(source any) (*Filesystem, error) {
	fs, ok := source.(*Filesystem)
	if ok {
		return fs, nil
	}

	// TODO: when returned by user actions, Filesystem is just a map[string]interface{}, need to fix, hack for now:

	m, ok := source.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid source type: %T", source)
	}
	id, ok := m["id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid source id: %T %v", source, source)
	}
	return &Filesystem{
		ID: FSID(id),
	}, nil
}
