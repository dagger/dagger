package dockerui

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/dagger/dagger/internal/buildkit/client/llb"
	"github.com/dagger/dagger/internal/buildkit/exporter/containerimage/exptypes"
	"github.com/dagger/dagger/internal/buildkit/frontend/gateway/client"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/patternmatcher/ignorefile"
	"github.com/pkg/errors"
)

const (
	contextPrefix       = "context:"
	inputMetadataPrefix = "input-metadata:"
	maxContextRecursion = 10
)

func (bc *Client) namedContext(ctx context.Context, name string, nameWithPlatform string, opt ContextOpt) (*llb.State, *dockerspec.DockerOCIImage, error) {
	return bc.namedContextRecursive(ctx, name, nameWithPlatform, opt, 0)
}

func (bc *Client) namedContextRecursive(ctx context.Context, name string, nameWithPlatform string, opt ContextOpt, count int) (*llb.State, *dockerspec.DockerOCIImage, error) {
	opts := bc.bopts.Opts
	contextKey := contextPrefix + nameWithPlatform
	v, ok := opts[contextKey]
	if !ok {
		return nil, nil, nil
	}

	if count > maxContextRecursion {
		return nil, nil, errors.New("context recursion limit exceeded; this may indicate a cycle in the provided source policies: " + v)
	}

	vv := strings.SplitN(v, ":", 2)
	if len(vv) != 2 {
		return nil, nil, errors.Errorf("invalid context specifier %s for %s", v, nameWithPlatform)
	}

	// allow git@ without protocol for SSH URLs for backwards compatibility
	if strings.HasPrefix(vv[0], "git@") {
		vv[0] = "git"
	}
	switch vv[0] {
	case "docker-image":
		return nil, nil, errors.Errorf("dockerfile named-context image sources are not supported: %s", v)
	case "git":
		st, ok := DetectGitContext(v, true)
		if !ok {
			return nil, nil, errors.Errorf("invalid git context %s", v)
		}
		return st, nil, nil
	case "http", "https":
		st, ok := DetectGitContext(v, true)
		if !ok {
			httpst := llb.HTTP(v, llb.WithCustomName("[context "+nameWithPlatform+"] "+v))
			st = &httpst
		}
		return st, nil, nil
	case "oci-layout":
		return nil, nil, errors.Errorf("dockerfile named-context OCI layout sources are not supported: %s", v)
	case "local":
		sessionID := bc.bopts.SessionID
		if v, ok := bc.localsSessionIDs[vv[1]]; ok {
			sessionID = v
		}
		st := llb.Local(vv[1],
			llb.SessionID(sessionID),
			llb.FollowPaths([]string{DefaultDockerignoreName}),
			llb.SharedKeyHint("context:"+nameWithPlatform+"-"+DefaultDockerignoreName),
			llb.WithCustomName("[context "+nameWithPlatform+"] load "+DefaultDockerignoreName),
			llb.Differ(llb.DiffNone, false),
		)
		def, err := st.Marshal(ctx)
		if err != nil {
			return nil, nil, err
		}
		res, err := bc.client.Solve(ctx, client.SolveRequest{
			Evaluate:   true,
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, nil, err
		}
		ref, err := res.SingleRef()
		if err != nil {
			return nil, nil, err
		}
		var excludes []string
		if !opt.NoDockerignore {
			dt, _ := ref.ReadFile(ctx, client.ReadRequest{
				Filename: DefaultDockerignoreName,
			}) // error ignored

			if len(dt) != 0 {
				excludes, err = ignorefile.ReadAll(bytes.NewBuffer(dt))
				if err != nil {
					return nil, nil, errors.Wrapf(err, "failed parsing %s", DefaultDockerignoreName)
				}
			}
		}

		localOutput := &asyncLocalOutput{
			name:             vv[1],
			nameWithPlatform: nameWithPlatform,
			sessionID:        sessionID,
			excludes:         excludes,
			extraOpts:        opt.AsyncLocalOpts,
		}
		st = llb.NewState(localOutput)
		return &st, nil, nil
	case "input":
		inputs, err := bc.client.Inputs(ctx)
		if err != nil {
			return nil, nil, err
		}
		st, ok := inputs[vv[1]]
		if !ok {
			return nil, nil, errors.Errorf("invalid input %s for %s", vv[1], nameWithPlatform)
		}
		md, ok := opts[inputMetadataPrefix+vv[1]]
		if ok {
			m := make(map[string][]byte)
			if err := json.Unmarshal([]byte(md), &m); err != nil {
				return nil, nil, errors.Wrapf(err, "failed to parse input metadata %s", md)
			}
			var img *dockerspec.DockerOCIImage
			if dtic, ok := m[exptypes.ExporterImageConfigKey]; ok {
				st, err = st.WithImageConfig(dtic)
				if err != nil {
					return nil, nil, err
				}
				if err := json.Unmarshal(dtic, &img); err != nil {
					return nil, nil, errors.Wrapf(err, "failed to parse image config for %s", nameWithPlatform)
				}
			}
			return &st, img, nil
		}
		return &st, nil, nil
	default:
		return nil, nil, errors.Errorf("unsupported context source %s for %s", vv[0], nameWithPlatform)
	}
}

// asyncLocalOutput is an llb.Output that computes an llb.Local
// on-demand instead of at the time of initialization.
type asyncLocalOutput struct {
	llb.Output
	name             string
	nameWithPlatform string
	sessionID        string
	excludes         []string
	extraOpts        func() []llb.LocalOption
	once             sync.Once
}

func (a *asyncLocalOutput) ToInput(ctx context.Context, constraints *llb.Constraints) (*pb.Input, error) {
	a.once.Do(a.do)
	return a.Output.ToInput(ctx, constraints)
}

func (a *asyncLocalOutput) Vertex(ctx context.Context, constraints *llb.Constraints) llb.Vertex {
	a.once.Do(a.do)
	return a.Output.Vertex(ctx, constraints)
}

func (a *asyncLocalOutput) do() {
	var extraOpts []llb.LocalOption
	if a.extraOpts != nil {
		extraOpts = a.extraOpts()
	}
	opts := append([]llb.LocalOption{
		llb.WithCustomName("[context " + a.nameWithPlatform + "] load from client"),
		llb.SessionID(a.sessionID),
		llb.SharedKeyHint("context:" + a.nameWithPlatform),
		llb.ExcludePatterns(a.excludes),
	}, extraOpts...)

	st := llb.Local(a.name, opts...)
	a.Output = st.Output()
}
