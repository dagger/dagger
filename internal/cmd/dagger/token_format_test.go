package daggercmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFmtTokenCount(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want string
	}{
		{0, "0"},
		{532, "532"},
		{999, "999"},
		{1000, "1.0k"},
		{45231, "45.2k"},
		{999999, "1000.0k"},
		{1000000, "1.0M"},
		{1234567, "1.2M"},
	} {
		require.Equal(t, tc.want, fmtTokenCount(tc.in), "fmtTokenCount(%d)", tc.in)
	}
}

func TestFmtTokenGrowth(t *testing.T) {
	for _, tc := range []struct {
		in   int
		want string
	}{
		{0, "no change"},
		{12100, "▲ +12.1k"},
		{-5000, "▼ -5.0k"},
		{500, "▲ +500"},
		{-999, "▼ -999"},
	} {
		require.Equal(t, tc.want, fmtTokenGrowth(tc.in), "fmtTokenGrowth(%d)", tc.in)
	}
}
