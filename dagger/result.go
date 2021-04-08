package dagger

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"dagger.io/go/dagger/compiler"
	"github.com/moby/buildkit/client/llb"
	"github.com/rs/zerolog/log"
)

const (
	planFile     = "plan.cue"
	inputFile    = "input.cue"
	computedFile = "computed.cue"
)

// DeploymentResult represents the layers of a deployment run
type DeploymentResult struct {
	// Layer 1: plan configuration
	plan *compiler.Value

	// Layer 2: user inputs
	input *compiler.Value

	// Layer 3: computed values
	computed *compiler.Value
}

func NewDeploymentResult() *DeploymentResult {
	return &DeploymentResult{
		plan:     compiler.NewValue(),
		input:    compiler.NewValue(),
		computed: compiler.NewValue(),
	}
}

func (r *DeploymentResult) Plan() *compiler.Value {
	return r.plan
}

func (r *DeploymentResult) Input() *compiler.Value {
	return r.input
}

func (r *DeploymentResult) Computed() *compiler.Value {
	return r.computed
}

func (r *DeploymentResult) Merge() (*compiler.Value, error) {
	return compiler.InstanceMerge(
		r.plan,
		r.input,
		r.computed,
	)
}

func (r *DeploymentResult) ToLLB() (llb.State, error) {
	st := llb.Scratch()

	planSource, err := r.plan.Source()
	if err != nil {
		return st, compiler.Err(err)
	}

	inputSource, err := r.input.Source()
	if err != nil {
		return st, compiler.Err(err)
	}

	outputSource, err := r.computed.Source()
	if err != nil {
		return st, compiler.Err(err)
	}

	st = st.
		File(
			llb.Mkfile(planFile, 0600, planSource),
			llb.WithCustomName("[internal] serializing plan"),
		).
		File(
			llb.Mkfile(inputFile, 0600, inputSource),
			llb.WithCustomName("[internal] serializing input"),
		).
		File(
			llb.Mkfile(computedFile, 0600, outputSource),
			llb.WithCustomName("[internal] serializing output"),
		)

	return st, nil
}

func ReadDeploymentResult(ctx context.Context, r io.Reader) (*DeploymentResult, error) {
	lg := log.Ctx(ctx)
	result := NewDeploymentResult()
	tr := tar.NewReader(r)

	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar stream: %w", err)
		}

		lg := lg.
			With().
			Str("file", h.Name).
			Logger()

		if !strings.HasSuffix(h.Name, ".cue") {
			lg.Debug().Msg("skipping non-cue file from exporter tar stream")
			continue
		}

		lg.Debug().Msg("outputfn: compiling")

		src, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}

		v, err := compiler.Compile(h.Name, src)
		if err != nil {
			lg.
				Debug().
				Err(compiler.Err(err)).
				Bytes("src", src).
				Msg("invalid result file")
			return nil, fmt.Errorf("failed to compile result: %w", compiler.Err(err))
		}

		switch h.Name {
		case planFile:
			result.plan = v
		case inputFile:
			result.input = v
		case computedFile:
			result.computed = v
		default:
			lg.Warn().Msg("unexpected file")
		}
	}
	return result, nil
}
