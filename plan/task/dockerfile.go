package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	bkplatforms "github.com/containerd/containerd/platforms"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	dockerfilebuilder "github.com/moby/buildkit/frontend/dockerfile/builder"
	"github.com/moby/buildkit/frontend/dockerfile/dockerfile2llb"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	bkpb "github.com/moby/buildkit/solver/pb"
	"github.com/rs/zerolog/log"

	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Dockerfile", func() Task { return &dockerfileTask{} })
}

type dockerfileTask struct {
}

func (t *dockerfileTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	lg := log.Ctx(ctx)
	auths, err := v.Lookup("auth").Fields()
	if err != nil {
		return nil, err
	}

	for _, auth := range auths {
		// Read auth info
		a, err := decodeAuthValue(pctx, auth.Value)
		if err != nil {
			return nil, err
		}
		// Extract registry target from dest
		target, err := solver.ParseAuthHost(auth.Label())
		if err != nil {
			return nil, err
		}
		s.AddCredentials(target, a.Username, a.Secret.PlainText())
		lg.Debug().Str("target", target).Msg("add target credentials")
	}

	source, err := pctx.FS.FromValue(v.Lookup("source"))
	if err != nil {
		return nil, err
	}

	sourceSt, err := source.State()
	if err != nil {
		return nil, err
	}

	// docker build context
	contextDef, err := s.Marshal(ctx, sourceSt)
	if err != nil {
		return nil, err
	}
	// Dockerfile context, default to docker build context
	dockerfileDef := contextDef

	// Support inlined dockerfile
	if dockerfile := v.Lookup("dockerfile.contents"); dockerfile.Exists() {
		contents, err := dockerfile.String()
		if err != nil {
			return nil, err
		}
		dockerfileDef, err = s.Marshal(ctx,
			llb.Scratch().File(
				llb.Mkfile("/Dockerfile", 0644, []byte(contents)),
			),
		)
		if err != nil {
			return nil, err
		}
	}

	opts, err := t.dockerBuildOpts(v, pctx)
	if err != nil {
		return nil, err
	}
	// Handle --no-cache
	if s.NoCache() {
		opts["no-cache"] = ""
	}

	req := bkgw.SolveRequest{
		Frontend:    "dockerfile.v0",
		FrontendOpt: opts,
		FrontendInputs: map[string]*bkpb.Definition{
			dockerfilebuilder.DefaultLocalNameContext:    contextDef,
			dockerfilebuilder.DefaultLocalNameDockerfile: dockerfileDef,
		},
	}
	res, err := s.SolveRequest(ctx, req)
	if err != nil {
		return nil, err
	}
	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	solvedRef := ref
	if ref != nil {
		st, err := ref.ToState()
		if err != nil {
			return nil, err
		}

		solvedRef, err = s.Solve(ctx, st, pctx.Platform.Get())
		if err != nil {
			return nil, err
		}
	}

	// Image metadata
	meta, ok := res.Metadata[exptypes.ExporterImageConfigKey]
	if !ok {
		return nil, errors.New("build returned no image config")
	}
	var image dockerfile2llb.Image
	if err := json.Unmarshal(meta, &image); err != nil {
		return nil, fmt.Errorf("failed to unmarshal image config: %w", err)
	}

	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": pctx.FS.New(solvedRef).MarshalCUE(),
		"config": ConvertImageConfig(image.Config),
	})
}

func (t *dockerfileTask) dockerBuildOpts(v *compiler.Value, pctx *plancontext.Context) (map[string]string, error) {
	opts := map[string]string{}

	if dockerfilePath := v.Lookup("dockerfile.path"); dockerfilePath.Exists() {
		filename, err := dockerfilePath.String()
		if err != nil {
			return nil, err
		}
		opts["filename"] = filename
	}

	if target := v.Lookup("target"); target.Exists() {
		tgr, err := target.String()
		if err != nil {
			return nil, err
		}
		opts["target"] = tgr
	}

	if hosts := v.Lookup("hosts"); hosts.Exists() {
		p := []string{}
		fields, err := hosts.Fields()
		if err != nil {
			return nil, err
		}
		for _, host := range fields {
			s, err := host.Value.String()
			if err != nil {
				return nil, err
			}
			p = append(p, host.Label()+"="+s)
		}
		if len(p) > 0 {
			opts["add-hosts"] = strings.Join(p, ",")
		}
	}

	if buildArgs := v.Lookup("buildArg"); buildArgs.Exists() {
		fields, err := buildArgs.Fields()
		if err != nil {
			return nil, err
		}
		for _, buildArg := range fields {
			s, err := buildArg.Value.String()
			if err != nil {
				return nil, err
			}
			opts["build-arg:"+buildArg.Label()] = s
		}
	}

	if labels := v.Lookup("label"); labels.Exists() {
		fields, err := labels.Fields()
		if err != nil {
			return nil, err
		}
		for _, label := range fields {
			s, err := label.Value.String()
			if err != nil {
				return nil, err
			}
			opts["label:"+label.Label()] = s
		}
	}

	if platforms := v.Lookup("platforms"); platforms.Exists() {
		p := []string{}
		list, err := platforms.List()
		if err != nil {
			return nil, err
		}

		for _, platform := range list {
			s, err := platform.String()
			if err != nil {
				return nil, err
			}
			p = append(p, s)
		}

		if len(p) > 0 {
			opts["platform"] = strings.Join(p, ",")
		}
		if len(p) > 1 {
			opts["multi-platform"] = "true"
		}
	}
	// Set platform to configured one if no one is defined
	if opts["platform"] == "" {
		opts["platform"] = bkplatforms.Format(pctx.Platform.Get())
	}

	return opts, nil
}
