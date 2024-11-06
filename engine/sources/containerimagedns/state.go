package containerimagedns

import (
	"context"
	"strconv"

	"github.com/distribution/reference"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/apicaps"
)

func Image(ref string, namespace string, opts ...llb.ImageOption) llb.State {
	r, err := reference.ParseNormalizedNamed(ref)
	if err == nil {
		r = reference.TagNameOnly(r)
		ref = r.String()
	}
	var info ImageInfo
	for _, opt := range opts {
		opt.SetImageOption(&info.ImageInfo)
	}

	addCap(&info.Constraints, pb.CapSourceImage)

	attrs := map[string]string{}
	if info.resolveMode != 0 {
		attrs[pb.AttrImageResolveMode] = info.resolveMode.String()
		if info.resolveMode == ResolveModeForcePull {
			addCap(&info.Constraints, pb.CapSourceImageResolveMode) // only require cap for security enforced mode
		}
	}

	if info.RecordType != "" {
		attrs[pb.AttrImageRecordType] = info.RecordType
	}

	if ll := info.layerLimit; ll != nil {
		attrs[pb.AttrImageLayerLimit] = strconv.FormatInt(int64(*ll), 10)
		addCap(&info.Constraints, pb.CapSourceImageLayerLimit)
	}

	src := llb.NewSource("docker-image://"+ref, attrs, info.Constraints) // controversial
	if err != nil {
		src.err = err
	} else if info.metaResolver != nil {
		if _, ok := r.(reference.Digested); ok || !info.resolveDigest {
			return NewState(src.Output()).Async(func(ctx context.Context, st State, c *Constraints) (State, error) {
				p := info.Constraints.Platform
				if p == nil {
					p = c.Platform
				}
				_, _, dt, err := info.metaResolver.ResolveImageConfig(ctx, ref, sourceresolver.Opt{
					Platform: p,
					ImageOpt: &sourceresolver.ResolveImageOpt{
						ResolveMode: info.resolveMode.String(),
					},
				})
				if err != nil {
					return State{}, err
				}
				return st.WithImageConfig(dt)
			})
		}
		return Scratch().Async(func(ctx context.Context, _ State, c *Constraints) (State, error) {
			p := info.Constraints.Platform
			if p == nil {
				p = c.Platform
			}
			ref, dgst, dt, err := info.metaResolver.ResolveImageConfig(context.TODO(), ref, sourceresolver.Opt{
				Platform: p,
				ImageOpt: &sourceresolver.ResolveImageOpt{
					ResolveMode: info.resolveMode.String(),
				},
			})
			if err != nil {
				return State{}, err
			}
			r, err := reference.ParseNormalizedNamed(ref)
			if err != nil {
				return State{}, err
			}
			if dgst != "" {
				r, err = reference.WithDigest(r, dgst)
				if err != nil {
					return State{}, err
				}
			}
			return NewState(NewSource("docker-image://"+r.String(), attrs, info.Constraints).Output()).WithImageConfig(dt)
		})
	}
	return NewState(src.Output())
}

func addCap(c *llb.Constraints, id apicaps.CapID) {
	if c.Metadata.Caps == nil {
		c.Metadata.Caps = make(map[apicaps.CapID]bool)
	}
	c.Metadata.Caps[id] = true
}

type ImageInfo struct {
	llb.ImageInfo
}
