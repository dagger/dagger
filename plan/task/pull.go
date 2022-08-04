package task

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/docker/distribution/reference"
	"github.com/moby/buildkit/client/llb"
	"github.com/rs/zerolog/log"
	"go.dagger.io/dagger/compiler"
	"go.dagger.io/dagger/plancontext"
	"go.dagger.io/dagger/solver"
)

func init() {
	Register("Pull", func() Task { return &pullTask{} })
}

type pullTask struct {
}

func (c *pullTask) Run(ctx context.Context, pctx *plancontext.Context, s *solver.Solver, v *compiler.Value) (*compiler.Value, error) {
	lg := log.Ctx(ctx)

	rawRef, err := v.Lookup("source").String()
	if err != nil {
		return nil, err
	}

	// Extract registry target from source
	target, err := solver.ParseAuthHost(rawRef)
	if err != nil {
		return nil, err
	}

	// Read auth info
	if auth := v.Lookup("auth"); auth.Exists() {
		a, err := decodeAuthValue(pctx, auth)
		if err != nil {
			return nil, err
		}

		s.AddCredentials(target, a.Username, a.Secret.PlainText())
		lg.Debug().Str("target", target).Msg("add target credentials")
	} else if target == "docker.io" {
		// Collect DOCKERHUB_AUTH_USER && DOCKERHUB_AUTH_PASSWORD env vars
		username, secret := "", ""
		for _, envVar := range os.Environ() {
			split := strings.SplitN(envVar, "=", 2)
			if len(split) != 2 {
				continue
			}
			key, val := split[0], split[1]
			if strings.EqualFold(key, "dockerhub_auth_user") {
				username = val
			}
			if strings.EqualFold(key, "dockerhub_auth_password") {
				secret = val
			}
		}

		if username != "" && secret != "" {
			s.AddCredentials(target, username, secret)
			lg.Debug().Str("target", target).Msg("add global credentials from DOCKERHUB_AUTH_USER and DOCKERHUB_AUTH_PASSWORD env vars")
		}
	}

	ref, err := reference.ParseNormalizedNamed(rawRef)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ref %s: %w", rawRef, err)
	}
	// Add the default tag "latest" to a reference if it only has a repo name.
	ref = reference.TagNameOnly(ref)

	var resolveMode llb.ResolveMode
	resolveModeValue, err := v.Lookup("resolveMode").String()
	if err != nil {
		return nil, err
	}

	switch resolveModeValue {
	case "default":
		resolveMode = llb.ResolveModeDefault
	case "forcePull":
		resolveMode = llb.ResolveModeForcePull
	case "preferLocal":
		resolveMode = llb.ResolveModePreferLocal
	default:
		return nil, fmt.Errorf("unknown resolve mode for %s: %s", rawRef, resolveModeValue)
	}

	st := llb.Image(
		ref.String(),
		withCustomName(v, "Pull %s", rawRef),
		resolveMode,
	)

	// Load image metadata and convert to to LLB.
	platform := pctx.Platform.Get()
	image, digest, err := s.ResolveImageConfig(ctx, ref.String(), llb.ResolveImageConfigOpt{
		LogName:     resolveImageConfigLogName(v, "load metadata for %s", ref.String()),
		Platform:    &platform,
		ResolveMode: resolveMode.String(),
	})
	if err != nil {
		return nil, err
	}

	result, err := s.Solve(ctx, st, pctx.Platform.Get())
	if err != nil {
		return nil, err
	}

	fs := pctx.FS.New(result)
	return compiler.NewValue().FillFields(map[string]interface{}{
		"output": fs.MarshalCUE(),
		"digest": digest,
		"config": ConvertImageConfig(image.Config),
	})
}
