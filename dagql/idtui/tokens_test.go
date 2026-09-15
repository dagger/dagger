package idtui

import (
	"testing"

	"github.com/muesli/termenv"
)

func TestFmtTokens(t *testing.T) {
	for _, tc := range []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{532, "532"},
		{999, "999"},
		{1000, "1.0k"},
		{12345, "12.3k"},
		{45231, "45.2k"},
		{1000000, "1.0M"},
		{1234567, "1.2M"},
	} {
		if got := fmtTokens(tc.in); got != tc.want {
			t.Errorf("fmtTokens(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestTokenSizeColor(t *testing.T) {
	for _, tc := range []struct {
		in   int64
		want termenv.Color
	}{
		{0, faintColor},
		{500, faintColor},
		{1999, faintColor},
		{2000, mbColor},
		{9999, mbColor},
		{10000, bigColor},
		{123456, bigColor},
	} {
		if got := tokenSizeColor(tc.in); got != tc.want {
			t.Errorf("tokenSizeColor(%d) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
