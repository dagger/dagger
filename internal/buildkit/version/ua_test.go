package version

import "testing"

func TestUserAgent(t *testing.T) {
	cases := []struct {
		name     string
		version  string
		products map[string]string
		want     string
	}{
		{
			name:    "dev",
			version: defaultVersion,
			want:    "buildkit/v0.0-dev",
		},
		{
			name:    "unknown",
			version: "0.0.0+unknown",
			want:    "buildkit/v0.0.0+unknown",
		},
		{
			name:    "release",
			version: "v0.11.6",
			want:    "buildkit/v0.11",
		},
		{
			name:    "product",
			version: "v0.11.6",
			products: map[string]string{
				"moby": "v24.0",
			},
			want: "buildkit/v0.11 moby/v24.0",
		},
	}
	for _, tt := range cases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			Version = tt.version
			for pname, pver := range tt.products {
				SetUserAgentProduct(pname, func() string {
					return pver
				})
			}
			if g, w := UserAgent(), tt.want; g != w {
				t.Fatalf("got: %q\nwant: %q", g, w)
			}
		})
	}
}
