package containerimagedns

import (
	"context"
	"fmt"
	"maps"
	"strconv"

	"github.com/distribution/reference"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/moby/buildkit/solver/pb"
	"github.com/moby/buildkit/util/apicaps"
)

func Image(ref string, namespace string, opts ...ImageOption) llb.State {
	r, err := reference.ParseNormalizedNamed(ref)
	if err == nil {
		r = reference.TagNameOnly(r)
		ref = r.String()
	}
	var info ImageInfo
	for _, opt := range opts {
		opt.SetImageOption(&info)
	}

	addCap(&info.Constraints, pb.CapSourceImage)

	attrs := map[string]string{}
	if info.resolveMode != 0 {
		attrs[pb.AttrImageResolveMode] = info.resolveMode.String()
		if info.resolveMode == llb.ResolveModeForcePull {
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

	attrs[AttrDNSNamespace] = namespace
	src := llb.NewSource("docker-image://"+ref, attrs, info.Constraints) // controversial
	if err != nil {
		// braa: this is very jank, but without reimplementing sourceop, we can't propogate errors because err is private.
		// instead, we revert to the llb implementation on the error path, thereby not propogating the dns namespace
		src.err = err
	} else if info.metaResolver != nil {
		if _, ok := r.(reference.Digested); ok || !info.resolveDigest {
			return llb.NewState(src.Output()).Async(func(ctx context.Context, st llb.State, c *llb.Constraints) (llb.State, error) {
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
					return llb.State{}, err
				}
				return st.WithImageConfig(dt)
			})
		}
		return llb.Scratch().Async(func(ctx context.Context, _ llb.State, c *llb.Constraints) (llb.State, error) {
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
				return llb.State{}, err
			}
			r, err := reference.ParseNormalizedNamed(ref)
			if err != nil {
				return llb.State{}, err
			}
			if dgst != "" {
				r, err = reference.WithDigest(r, dgst)
				if err != nil {
					return llb.State{}, err
				}
			}
			return llb.NewState(llb.NewSource("docker-image://"+r.String(), attrs, info.Constraints).Output()).WithImageConfig(dt)
		})
	}
	return llb.NewState(src.Output())
}

func addCap(c *llb.Constraints, id apicaps.CapID) {
	if c.Metadata.Caps == nil {
		c.Metadata.Caps = make(map[apicaps.CapID]bool)
	}
	c.Metadata.Caps[id] = true
}

type ImageInfo struct {
	constraintsWrapper
	metaResolver  llb.ImageMetaResolver
	resolveDigest bool
	resolveMode   llb.ResolveMode
	layerLimit    *int
	RecordType    string
}

type constraintsWrapper struct {
	llb.Constraints
}

type ImageOption interface {
	SetImageOption(*ImageInfo)
}

func WithCustomNamef(name string, a ...interface{}) llb.ConstraintsOpt {
	return WithCustomName(fmt.Sprintf(name, a...))
}

func WithCustomName(name string) llb.ConstraintsOpt {
	return WithDescription(map[string]string{
		"llb.customname": name,
	})
}

func WithDescription(m map[string]string) llb.ConstraintsOpt {
	return constraintsOptFunc(func(c *llb.Constraints) {
		if c.Metadata.Description == nil {
			c.Metadata.Description = map[string]string{}
		}
		maps.Copy(c.Metadata.Description, m)
	})
}

type constraintsOptFunc func(m *llb.Constraints)

func (fn constraintsOptFunc) SetConstraintsOption(m *llb.Constraints) {
	fn(m)
}

func (fn constraintsOptFunc) SetRunOption(ei *llb.ExecInfo) {
	ei.applyConstraints(fn)
}

func (fn constraintsOptFunc) SetLocalOption(li *llb.LocalInfo) {
	li.applyConstraints(fn)
}

func (fn constraintsOptFunc) SetOCILayoutOption(oi *llb.OCILayoutInfo) {
	oi.applyConstraints(fn)
}

func (fn constraintsOptFunc) SetHTTPOption(hi *llb.HTTPInfo) {
	hi.applyConstraints(fn)
}

func (fn constraintsOptFunc) SetImageOption(ii *llb.ImageInfo) {
	ii.applyConstraints(fn)
}

func (fn constraintsOptFunc) SetGitOption(gi *llb.GitInfo) {
	gi.applyConstraints(fn)
}

func (cw *constraintsWrapper) applyConstraints(f func(c *llb.Constraints)) {
	f(&cw.Constraints)
}
