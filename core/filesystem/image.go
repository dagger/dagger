package filesystem

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/solver/pb"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

// Image pairs a filesystem LLB definition with an image config providing
// defaults for future commands.
type Image struct {
	FS     *pb.Definition    `json:"fs"`
	Config specs.ImageConfig `json:"cfg"`
}

func (info *Image) ToFilesystem() (*Filesystem, error) {
	jsonBytes, err := json.Marshal(info)
	if err != nil {
		return nil, err
	}
	b64Bytes := make([]byte, base64.StdEncoding.EncodedLen(len(jsonBytes)))
	base64.StdEncoding.Encode(b64Bytes, jsonBytes)
	return &Filesystem{
		ID: FSID(b64Bytes),
	}, nil
}

func (info *Image) ToState() (llb.State, error) {
	defop, err := llb.NewDefinitionOp(info.FS)
	if err != nil {
		return llb.State{}, err
	}

	st := llb.NewState(defop)
	for _, env := range info.Config.Env {
		parts := strings.SplitN(env, "=", 2)
		if len(parts[0]) > 0 {
			var v string
			if len(parts) > 1 {
				v = parts[1]
			}
			st = st.AddEnv(parts[0], v)
		}
	}

	st = st.Dir(info.Config.WorkingDir)

	return st, nil
}

// ImageFromState returns an Image using the LLB state as both the filesystem
// and the source of image config.
func ImageFromState(ctx context.Context, st llb.State, marshalOpts ...llb.ConstraintsOpt) (*Image, error) {
	cfg, err := ImageConfigFromState(ctx, st)
	if err != nil {
		return nil, err
	}
	return ImageFromStateAndConfig(ctx, st, cfg, marshalOpts...)
}

// ImageConfigFromState generates an OCI image config from values configured in
// LLB state (currently env and workdir).
func ImageConfigFromState(ctx context.Context, st llb.State) (specs.ImageConfig, error) {
	env, err := st.Env(ctx)
	if err != nil {
		return specs.ImageConfig{}, err
	}
	dir, err := st.GetDir(ctx)
	if err != nil {
		return specs.ImageConfig{}, err
	}
	return specs.ImageConfig{
		Env:        env,
		WorkingDir: dir,
	}, nil
}

// ImageFromStateAndConfig returns an Image using the LLB state for the
// filesystem, paired with a given image config.
func ImageFromStateAndConfig(ctx context.Context, st llb.State, cfg specs.ImageConfig, marshalOpts ...llb.ConstraintsOpt) (*Image, error) {
	def, err := st.Marshal(ctx, marshalOpts...)
	if err != nil {
		return nil, err
	}
	return &Image{
		FS:     def.ToPB(),
		Config: cfg,
	}, nil
}
