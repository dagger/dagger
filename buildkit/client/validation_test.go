package client

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	"github.com/moby/buildkit/frontend/gateway/client"
	sppb "github.com/moby/buildkit/sourcepolicy/pb"
	"github.com/moby/buildkit/util/testutil/integration"
	"github.com/moby/buildkit/util/testutil/workers"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

var validationTests = []func(t *testing.T, sb integration.Sandbox){
	testValidateNullConfig,
	testValidateInvalidConfig,
	testValidatePlatformsEmpty,
	testValidatePlatformsInvalid,
	testValidateSourcePolicy,
}

func testValidateNullConfig(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)
	workers.CheckFeatureCompat(t, sb, workers.FeatureOCIExporter)

	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		def, err := llb.Scratch().Marshal(ctx)
		if err != nil {
			return nil, err
		}

		res, err := c.Solve(ctx, client.SolveRequest{
			Evaluate:   true,
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, err
		}
		res.AddMeta("containerimage.config", []byte("null"))
		return res, nil
	}

	_, err = c.Build(ctx, SolveOpt{
		Exports: []ExportEntry{
			{
				Type:   ExporterOCI,
				Output: fixedWriteCloser(nopWriteCloser{io.Discard}),
			},
		},
	}, "", b, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid null image config for export")
}

func testValidateInvalidConfig(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)
	workers.CheckFeatureCompat(t, sb, workers.FeatureOCIExporter)

	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		def, err := llb.Scratch().Marshal(ctx)
		if err != nil {
			return nil, err
		}

		res, err := c.Solve(ctx, client.SolveRequest{
			Evaluate:   true,
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, err
		}
		var img ocispecs.Image
		img.Platform = ocispecs.Platform{
			Architecture: "amd64",
		}
		dt, err := json.Marshal(img)
		if err != nil {
			return nil, err
		}
		res.AddMeta("containerimage.config", dt)
		return res, nil
	}

	_, err = c.Build(ctx, SolveOpt{
		Exports: []ExportEntry{
			{
				Type:   ExporterOCI,
				Output: fixedWriteCloser(nopWriteCloser{io.Discard}),
			},
		},
	}, "", b, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid image config: os and architecture must be specified together")
}

func testValidatePlatformsEmpty(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)
	workers.CheckFeatureCompat(t, sb, workers.FeatureOCIExporter)

	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	b := func(ctx context.Context, c client.Client) (*client.Result, error) {
		def, err := llb.Scratch().Marshal(ctx)
		if err != nil {
			return nil, err
		}

		res, err := c.Solve(ctx, client.SolveRequest{
			Evaluate:   true,
			Definition: def.ToPB(),
		})
		if err != nil {
			return nil, err
		}
		res.AddMeta("refs.platforms", []byte("null"))
		return res, nil
	}

	_, err = c.Build(ctx, SolveOpt{
		Exports: []ExportEntry{
			{
				Type:   ExporterOCI,
				Output: fixedWriteCloser(nopWriteCloser{io.Discard}),
			},
		},
	}, "", b, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid empty platforms index for exporter")
}

func testValidatePlatformsInvalid(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)
	workers.CheckFeatureCompat(t, sb, workers.FeatureOCIExporter)

	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	tcases := []struct {
		name  string
		value []exptypes.Platform
		exp   string
	}{
		{
			name:  "emptyID",
			value: []exptypes.Platform{{}},
			exp:   "invalid empty platform key for exporter",
		},
		{
			name: "missingOS",
			value: []exptypes.Platform{
				{
					ID: "foo",
				},
			},
			exp: "invalid platform value",
		},
	}

	for _, tc := range tcases {
		t.Run(tc.name, func(t *testing.T) {
			b := func(ctx context.Context, c client.Client) (*client.Result, error) {
				def, err := llb.Scratch().Marshal(ctx)
				if err != nil {
					return nil, err
				}

				res, err := c.Solve(ctx, client.SolveRequest{
					Evaluate:   true,
					Definition: def.ToPB(),
				})
				if err != nil {
					return nil, err
				}

				dt, err := json.Marshal(exptypes.Platforms{Platforms: tc.value})
				if err != nil {
					return nil, err
				}

				res.AddMeta("refs.platforms", dt)
				return res, nil
			}

			_, err = c.Build(ctx, SolveOpt{
				Exports: []ExportEntry{
					{
						Type:   ExporterOCI,
						Output: fixedWriteCloser(nopWriteCloser{io.Discard}),
					},
				},
			}, "", b, nil)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.exp)
		})
	}
}

func testValidateSourcePolicy(t *testing.T, sb integration.Sandbox) {
	requiresLinux(t)

	ctx := sb.Context()

	c, err := New(ctx, sb.Address())
	require.NoError(t, err)
	defer c.Close()

	tcases := []struct {
		name  string
		value *sppb.Policy
		exp   string
	}{
		// this condition fails on marshaling atm
		// {
		// 	name: "nilrule",
		// 	value: &sppb.Policy{
		// 		Rules: []*sppb.Rule{nil},
		// 	},
		// 	exp: "",
		// },
		{
			name: "nilselector",
			value: &sppb.Policy{
				Rules: []*sppb.Rule{
					{
						Action: sppb.PolicyAction_CONVERT,
					},
				},
			},
			exp: "invalid nil selector in policy",
		},
		{
			name: "emptyaction",
			value: &sppb.Policy{
				Rules: []*sppb.Rule{
					{
						Action: sppb.PolicyAction(9000),
						Selector: &sppb.Selector{
							Identifier: "docker-image://docker.io/library/alpine:latest",
						},
					},
				},
			},
			exp: "unknown type",
		},
		{
			name: "nilupdates",
			value: &sppb.Policy{
				Rules: []*sppb.Rule{
					{
						Action: sppb.PolicyAction_CONVERT,
						Selector: &sppb.Selector{
							Identifier: "docker-image://docker.io/library/alpine:latest",
						},
					},
				},
			},
			exp: "missing destination for convert rule",
		},
	}

	for _, tc := range tcases {
		t.Run(tc.name, func(t *testing.T) {
			var viaFrontend bool

			b := func(ctx context.Context, c client.Client) (*client.Result, error) {
				def, err := llb.Image("alpine").Marshal(ctx)
				if err != nil {
					return nil, err
				}

				req := client.SolveRequest{
					Evaluate:   true,
					Definition: def.ToPB(),
				}
				if viaFrontend {
					req.SourcePolicies = []*sppb.Policy{
						tc.value,
					}
				}
				return c.Solve(ctx, req)
			}

			_, err = c.Build(ctx, SolveOpt{
				SourcePolicy: tc.value,
			}, "", b, nil)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.exp)

			viaFrontend = true
			_, err = c.Build(ctx, SolveOpt{}, "", b, nil)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.exp)
		})
	}
}
