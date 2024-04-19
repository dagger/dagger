package sourcepolicy

import (
	"context"
	"testing"

	"github.com/moby/buildkit/solver/pb"
	spb "github.com/moby/buildkit/sourcepolicy/pb"
	"github.com/moby/buildkit/util/bklog"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestEngineEvaluate(t *testing.T) {
	t.Run("Deny All", testDenyAll)
	t.Run("Allow Deny", testAllowDeny)
	t.Run("Convert", testConvert)
	t.Run("Convert Deny", testConvertDeny)
	t.Run("Allow Convert Deny", testAllowConvertDeny)
	t.Run("Test convert loop", testConvertLoop)
	t.Run("Test convert http", testConvertHTTP)
	t.Run("Test convert regex", testConvertRegex)
	t.Run("Test convert wildcard", testConvertWildcard)
	t.Run("Test convert multiple", testConvertMultiple)
	t.Run("test multiple policies", testMultiplePolicies)
	t.Run("Last rule wins", testLastRuleWins)
}

func testLastRuleWins(t *testing.T) {
	pol := []*spb.Policy{
		{
			Rules: []*spb.Rule{
				{
					Action: spb.PolicyAction_ALLOW,
					Selector: &spb.Selector{
						Identifier: "docker-image://docker.io/library/busybox:latest",
					},
				},
				{
					Action: spb.PolicyAction_DENY,
					Selector: &spb.Selector{
						Identifier: "docker-image://docker.io/library/busybox:latest",
					},
				},
				{
					Action: spb.PolicyAction_ALLOW,
					Selector: &spb.Selector{
						Identifier: "docker-image://docker.io/library/busybox:latest",
					},
				},
			},
		},
	}

	e := NewEngine(pol)
	mut, err := e.Evaluate(context.Background(), &pb.SourceOp{
		Identifier: "docker-image://docker.io/library/busybox:latest",
	})
	require.NoError(t, err)
	require.False(t, mut)
}

func testMultiplePolicies(t *testing.T) {
	pol := []*spb.Policy{
		{
			Rules: []*spb.Rule{
				{
					Action: spb.PolicyAction_ALLOW,
					Selector: &spb.Selector{
						Identifier: "docker-image://docker.io/library/busybox:latest",
					},
				},
			},
		},
		{
			Rules: []*spb.Rule{
				{
					Action: spb.PolicyAction_DENY,
					Selector: &spb.Selector{
						Identifier: "docker-image://docker.io/library/busybox:latest",
					},
				},
			},
		},
	}

	e := NewEngine(pol)
	mut, err := e.Evaluate(context.Background(), &pb.SourceOp{
		Identifier: "docker-image://docker.io/library/busybox:latest",
	})
	require.ErrorIs(t, err, ErrSourceDenied)
	require.False(t, mut)
}

func testConvertMultiple(t *testing.T) {
	pol := []*spb.Policy{
		{
			Rules: []*spb.Rule{
				{
					Action: spb.PolicyAction_CONVERT,
					Selector: &spb.Selector{
						Identifier: "docker-image://docker.io/library/busybox:latest",
					},
					Updates: &spb.Update{
						Identifier: "docker-image://docker.io/library/alpine:latest",
					},
				},
				{
					Action: spb.PolicyAction_CONVERT,
					Selector: &spb.Selector{
						Identifier: "docker-image://docker.io/library/alpine:latest",
					},
					Updates: &spb.Update{
						Identifier: "docker-image://docker.io/library/debian:buster",
					},
				},
				{
					Action: spb.PolicyAction_CONVERT,
					Selector: &spb.Selector{
						Identifier: "docker-image://docker.io/library/debian:buster",
					},
					Updates: &spb.Update{
						Identifier: "docker-image://docker.io/library/debian:bullseye",
					},
				},
			},
		},
	}

	op := &pb.SourceOp{
		Identifier: "docker-image://docker.io/library/busybox:latest",
	}

	ctx := context.Background()
	e := NewEngine(pol)

	mutated, err := e.Evaluate(ctx, op)
	require.True(t, mutated)
	require.NoError(t, err)
}

func testConvertWildcard(t *testing.T) {
	pol := []*spb.Policy{
		{
			Rules: []*spb.Rule{
				{
					Action: spb.PolicyAction_CONVERT,
					Selector: &spb.Selector{
						Identifier: "docker-image://docker.io/library/golang:*",
						MatchType:  spb.MatchType_WILDCARD,
					},
					Updates: &spb.Update{
						Identifier: "docker-image://fakereg.io/library/golang:${1}",
					},
				},
			},
		},
	}

	op := &pb.SourceOp{
		Identifier: "docker-image://docker.io/library/golang:1.19",
	}

	ctx := context.Background()
	e := NewEngine(pol)

	mutated, err := e.Evaluate(ctx, op)
	require.True(t, mutated)
	require.NoError(t, err)
	require.Equal(t, "docker-image://fakereg.io/library/golang:1.19", op.Identifier)
}

func testConvertRegex(t *testing.T) {
	pol := &spb.Policy{
		Rules: []*spb.Rule{
			{
				Action: spb.PolicyAction_CONVERT,
				Selector: &spb.Selector{
					Identifier: `docker\-image://docker\.io/library/golang:(.*)`,
					MatchType:  spb.MatchType_REGEX,
				},
				Updates: &spb.Update{
					Identifier: "docker-image://fakereg.io/library/golang:${1}",
				},
			},
		},
	}

	op := &pb.SourceOp{
		Identifier: "docker-image://docker.io/library/golang:1.19",
	}

	ctx := context.Background()
	e := NewEngine([]*spb.Policy{pol})

	mutated, err := e.Evaluate(ctx, op)
	require.True(t, mutated)
	require.NoError(t, err)
	require.Equal(t, "docker-image://fakereg.io/library/golang:1.19", op.Identifier)
}

func testConvertHTTP(t *testing.T) {
	pol := &spb.Policy{
		Rules: []*spb.Rule{
			{
				Action: spb.PolicyAction_CONVERT,
				Selector: &spb.Selector{
					Identifier: "https://example.com/foo",
				},
				Updates: &spb.Update{
					Attrs: map[string]string{"http.checksum": "sha256:1234"},
				},
			},
		},
	}

	op := &pb.SourceOp{
		Identifier: "https://example.com/foo",
	}

	ctx := context.Background()
	e := NewEngine([]*spb.Policy{pol})

	mutated, err := e.Evaluate(ctx, op)
	require.True(t, mutated)
	require.NoError(t, err)
	require.Equal(t, "https://example.com/foo", op.Identifier)
}

func testConvertLoop(t *testing.T) {
	pol := &spb.Policy{
		Rules: []*spb.Rule{
			{
				Action: spb.PolicyAction_CONVERT,
				Selector: &spb.Selector{
					Identifier: "docker-image://docker.io/library/busybox:latest",
				},
				Updates: &spb.Update{
					Identifier: "docker-image://docker.io/library/alpine:latest",
				},
			},
			{
				Action: spb.PolicyAction_CONVERT,
				Selector: &spb.Selector{
					Identifier: "docker-image://docker.io/library/alpine:latest",
				},
				Updates: &spb.Update{
					Identifier: "docker-image://docker.io/library/busybox:latest",
				},
			},
		},
	}

	op := &pb.SourceOp{
		Identifier: "docker-image://docker.io/library/busybox:latest",
	}

	ctx := context.Background()
	e := NewEngine([]*spb.Policy{pol})

	mutated, err := e.Evaluate(ctx, op)
	require.True(t, mutated)
	require.ErrorIs(t, err, ErrTooManyOps)
}

func testAllowConvertDeny(t *testing.T) {
	pol := &spb.Policy{
		Rules: []*spb.Rule{
			{
				Action: spb.PolicyAction_CONVERT,
				Selector: &spb.Selector{
					Identifier: "docker-image://docker.io/library/busybox:latest",
				},
				Updates: &spb.Update{
					Identifier: "docker-image://docker.io/library/alpine:latest",
				},
			},
			{
				Action: spb.PolicyAction_ALLOW,
				Selector: &spb.Selector{
					Identifier: "docker-image://docker.io/library/alpine:latest",
				},
			},
			{
				Action: spb.PolicyAction_DENY,
				Selector: &spb.Selector{
					Identifier: "docker-image://docker.io/library/alpine:*",
				},
			},
			{
				Action: spb.PolicyAction_DENY,
				Selector: &spb.Selector{
					Identifier: "docker-image://docker.io/library/busybox:latest",
				},
			},
		},
	}

	op := &pb.SourceOp{
		Identifier: "docker-image://docker.io/library/busybox:latest",
	}

	ctx := context.Background()
	e := NewEngine([]*spb.Policy{pol})

	mutated, err := e.Evaluate(ctx, op)
	require.True(t, mutated)
	require.ErrorIs(t, err, ErrSourceDenied)
	require.Equal(t, op.Identifier, "docker-image://docker.io/library/alpine:latest")
}

func testConvertDeny(t *testing.T) {
	pol := &spb.Policy{
		Rules: []*spb.Rule{
			{
				Action: spb.PolicyAction_DENY,
				Selector: &spb.Selector{
					Identifier: "docker-image://docker.io/library/alpine:*",
				},
			},
			{
				Action: spb.PolicyAction_CONVERT,
				Selector: &spb.Selector{
					Identifier: "docker-image://docker.io/library/busybox:latest",
				},
				Updates: &spb.Update{
					Identifier: "docker-image://docker.io/library/alpine:latest",
				},
			},
		},
	}

	op := &pb.SourceOp{
		Identifier: "docker-image://docker.io/library/busybox:latest",
	}

	ctx := context.Background()
	e := NewEngine([]*spb.Policy{pol})

	mutated, err := e.Evaluate(ctx, op)
	require.True(t, mutated)
	require.ErrorIs(t, err, ErrSourceDenied)
	require.Equal(t, op.Identifier, "docker-image://docker.io/library/alpine:latest")
}

func testConvert(t *testing.T) {
	cases := map[string]string{
		"docker-image://docker.io/library/busybox:latest": "docker-image://docker.io/library/alpine:latest",
		"docker-image://docker.io/library/alpine:latest":  "docker-image://docker.io/library/alpine:latest@sha256:c0d488a800e4127c334ad20d61d7bc21b4097540327217dfab52262adc02380c",
	}
	bklog.L.Logger.SetLevel(logrus.DebugLevel)

	for src, dst := range cases {
		t.Run(src+"=>"+dst, func(t *testing.T) {
			op := &pb.SourceOp{
				Identifier: src,
			}

			pol := &spb.Policy{
				Rules: []*spb.Rule{
					{
						Action: spb.PolicyAction_CONVERT,
						Selector: &spb.Selector{
							Identifier: src,
						},
						Updates: &spb.Update{
							Identifier: dst,
						},
					},
				},
			}

			ctx := context.Background()
			e := NewEngine([]*spb.Policy{pol})

			mutated, err := e.Evaluate(ctx, op)
			require.True(t, mutated)
			require.NoError(t, err)
			require.Equal(t, dst, op.Identifier)
		})
	}
}

func testAllowDeny(t *testing.T) {
	op := &pb.SourceOp{
		Identifier: "docker-image://docker.io/library/alpine:latest",
	}
	pol := &spb.Policy{
		Rules: []*spb.Rule{
			{
				Action: spb.PolicyAction_ALLOW,
				Selector: &spb.Selector{
					Identifier: "docker-image://docker.io/library/alpine:latest",
				},
			},
			{
				Action: spb.PolicyAction_DENY,
				Selector: &spb.Selector{
					Identifier: "docker-image://*",
				},
			},
		},
	}

	ctx := context.Background()
	e := NewEngine([]*spb.Policy{pol})

	mutated, err := e.Evaluate(ctx, op)
	require.False(t, mutated)
	require.ErrorIs(t, err, ErrSourceDenied)

	op = &pb.SourceOp{
		Identifier: "docker-image://docker.io/library/busybox:latest",
	}

	mutated, err = e.Evaluate(ctx, op)
	require.False(t, mutated)
	require.ErrorIs(t, err, ErrSourceDenied)
}

func testDenyAll(t *testing.T) {
	cases := map[string]string{
		"docker-image": "docker-image://docker.io/library/alpine:latest",
		"https":        "https://github.com/moby/buildkit.git",
		"http":         "http://example.com",
	}

	for kind, ref := range cases {
		t.Run(ref, func(t *testing.T) {
			pol := &spb.Policy{
				Rules: []*spb.Rule{
					{
						Action: spb.PolicyAction_DENY,
						Selector: &spb.Selector{
							Identifier: kind + "://*",
						},
					},
				},
			}

			e := NewEngine([]*spb.Policy{pol})
			ctx := context.Background()

			op := &pb.SourceOp{
				Identifier: ref,
			}

			mutated, err := e.Evaluate(ctx, op)
			require.False(t, mutated)
			require.ErrorIs(t, err, ErrSourceDenied)
		})
	}
}
