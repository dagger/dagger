package urlutil

import "testing"

func TestRedactCredentials(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "non-blank Password",
			url:  "https://user:password@host.tld/this:that",
			want: "https://xxxxx:xxxxx@host.tld/this:that",
		},
		{
			name: "blank Password",
			url:  "https://user@host.tld/this:that",
			want: "https://xxxxx@host.tld/this:that",
		},
		{
			name: "blank Username",
			url:  "https://:password@host.tld/this:that",
			want: "https://:xxxxx@host.tld/this:that",
		},
		{
			name: "blank Username, blank Password",
			url:  "https://host.tld/this:that",
			want: "https://host.tld/this:that",
		},
		{
			name: "invalid URL",
			url:  "1https://foo.com",
			want: "1https://foo.com",
		},
	}
	for _, tt := range cases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			if g, w := RedactCredentials(tt.url), tt.want; g != w {
				t.Fatalf("got: %q\nwant: %q", g, w)
			}
		})
	}
}
