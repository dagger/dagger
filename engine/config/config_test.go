package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMaxParallelismUnmarshal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		input   string
		want    MaxParallelism
		wantErr bool
	}{
		{name: "num", input: `{"num": 8}`, want: MaxParallelism{Num: 8}},
		{name: "cpu percentage", input: `{"cpu": 50}`, want: MaxParallelism{CPU: 50}},
		{name: "cpu full", input: `{"cpu": 100}`, want: MaxParallelism{CPU: 100}},
		{name: "empty", input: `{}`, want: MaxParallelism{}},
		{name: "num negative", input: `{"num": -1}`, wantErr: true},
		{name: "cpu too high", input: `{"cpu": 150}`, wantErr: true},
		{name: "cpu negative", input: `{"cpu": -1}`, wantErr: true},
		{name: "both strategies", input: `{"num": 8, "cpu": 50}`, wantErr: true},
		{name: "unknown strategy", input: `{"ram": 4}`, wantErr: true},
		{name: "not an object", input: `8`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got MaxParallelism
			err := got.UnmarshalJSON([]byte(tc.input))
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestMaxParallelismResolve(t *testing.T) {
	t.Parallel()
	var nilP *MaxParallelism
	require.Equal(t, 0, nilP.Resolve(8))

	for _, tc := range []struct {
		name   string
		p      MaxParallelism
		numCPU int
		want   int
	}{
		{name: "unbounded", p: MaxParallelism{}, numCPU: 16, want: 0},
		{name: "num", p: MaxParallelism{Num: 4}, numCPU: 16, want: 4},
		{name: "cpu full", p: MaxParallelism{CPU: 100}, numCPU: 16, want: 16},
		{name: "cpu half", p: MaxParallelism{CPU: 50}, numCPU: 16, want: 8},
		{name: "cpu rounds down", p: MaxParallelism{CPU: 50}, numCPU: 3, want: 1},
		{name: "cpu never below one", p: MaxParallelism{CPU: 10}, numCPU: 2, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.p.Resolve(tc.numCPU))
		})
	}
}

func TestMaxParallelismRoundTrip(t *testing.T) {
	t.Parallel()
	cfg, err := Load(strings.NewReader(`{"maxParallelism": {"cpu": 50}}`))
	require.NoError(t, err)
	require.NotNil(t, cfg.MaxParallelism)
	require.Equal(t, 50, cfg.MaxParallelism.CPU)
	require.Equal(t, 4, cfg.MaxParallelism.Resolve(8))

	var sb strings.Builder
	require.NoError(t, cfg.Save(&sb))
	require.Contains(t, sb.String(), `"maxParallelism":{"cpu":50}`)

	cfg, err = Load(strings.NewReader(`{"maxParallelism": {"num": 6}}`))
	require.NoError(t, err)
	require.Equal(t, 6, cfg.MaxParallelism.Num)
	require.Equal(t, 6, cfg.MaxParallelism.Resolve(8))
	sb.Reset()
	require.NoError(t, cfg.Save(&sb))
	require.Contains(t, sb.String(), `"maxParallelism":{"num":6}`)

	// unknown strategy keys are rejected by the top-level loader too
	_, err = Load(strings.NewReader(`{"maxParallelism": {"ram": 4}}`))
	require.Error(t, err)
}
