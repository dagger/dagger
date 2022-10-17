package schema

import (
	"context"
	"encoding/json"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/filesystem"
	"github.com/dagger/dagger/project"
	"github.com/dagger/dagger/router"
	bkclient "github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

type InitializeArgs struct {
	Router        *router.Router
	SSHAuthSockID string
	WorkdirID     core.HostDirectoryID
	Gateway       bkgw.Client
	BKClient      *bkclient.Client
	SolveOpts     bkclient.SolveOpt
	SolveCh       chan *bkclient.SolveStatus
	Platform      specs.Platform
}

func New(params InitializeArgs) (router.ExecutableSchema, error) {
	base := &baseSchema{
		router:        params.Router,
		gw:            params.Gateway,
		bkClient:      params.BKClient,
		solveOpts:     params.SolveOpts,
		solveCh:       params.SolveCh,
		platform:      params.Platform,
		sshAuthSockID: params.SSHAuthSockID,
	}
	return router.MergeExecutableSchemas("core",
		&coreSchema{base},
		&directorySchema{base},
		&fileSchema{base},
		&gitSchema{base},
		&containerSchema{base},
		&cacheSchema{base},
		&secretSchema{base},
		&hostSchema{base, params.WorkdirID},
		&filesystemSchema{base},
		&projectSchema{
			baseSchema:    base,
			projectStates: make(map[string]*project.State),
		},
		&execSchema{base},
		&dockerBuildSchema{base},
		&httpSchema{base},
	)
}

type baseSchema struct {
	router        *router.Router
	gw            bkgw.Client
	bkClient      *bkclient.Client
	solveOpts     bkclient.SolveOpt
	solveCh       chan *bkclient.SolveStatus
	platform      specs.Platform
	sshAuthSockID string
}

func (r *baseSchema) Solve(ctx context.Context, st llb.State, marshalOpts ...llb.ConstraintsOpt) (*filesystem.Filesystem, error) {
	marshalOpts = append([]llb.ConstraintsOpt{llb.Platform(r.platform)}, marshalOpts...)

	inputCfg, err := filesystem.ImageConfigFromState(ctx, st)
	if err != nil {
		return nil, err
	}

	input, err := st.Marshal(ctx, marshalOpts...)
	if err != nil {
		return nil, err
	}

	res, err := r.gw.Solve(ctx, bkgw.SolveRequest{
		Evaluate:   true,
		Definition: input.ToPB(),
	})
	if err != nil {
		return nil, err
	}

	bkref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	resSt, err := bkref.ToState()
	if err != nil {
		return nil, err
	}

	img, err := filesystem.ImageFromStateAndConfig(ctx, resSt, inputCfg, marshalOpts...)
	if err != nil {
		return nil, err
	}

	return img.ToFilesystem()
}

func (r *baseSchema) Export(ctx context.Context, fs *filesystem.Filesystem, export bkclient.ExportEntry) error {
	fsDef, err := fs.ToDefinition()
	if err != nil {
		return err
	}
	img, err := fs.ToImage()
	if err != nil {
		return err
	}
	cfgBytes, err := json.Marshal(specs.Image{
		Architecture: r.platform.Architecture,
		OS:           r.platform.OS,
		OSVersion:    r.platform.OSVersion,
		OSFeatures:   r.platform.OSFeatures,
		Config:       img.Config,
	})
	if err != nil {
		return err
	}

	solveOpts := r.solveOpts
	// NOTE: be careful to not overwrite any values from original shared r.solveOpts (i.e. with append).
	solveOpts.Exports = []bkclient.ExportEntry{export}

	// Mirror events from the sub-Build into the main Build event channel.
	// Build() will close the channel after completion so we don't want to use the main channel directly.
	ch := make(chan *bkclient.SolveStatus)
	go func() {
		for event := range ch {
			r.solveCh <- event
		}
	}()

	_, err = r.bkClient.Build(ctx, solveOpts, "", func(ctx context.Context, gw bkgw.Client) (*bkgw.Result, error) {
		res, err := gw.Solve(ctx, bkgw.SolveRequest{
			Evaluate:   true,
			Definition: fsDef,
		})
		if err != nil {
			return nil, err
		}
		res.AddMeta(exptypes.ExporterImageConfigKey, cfgBytes)
		return res, nil
	}, ch)
	if err != nil {
		return err
	}
	return nil
}
