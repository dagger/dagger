package sourcepolicy

import (
	"context"
	"testing"

	spb "github.com/moby/buildkit/sourcepolicy/pb"
	"github.com/stretchr/testify/require"
)

func TestMatch(t *testing.T) {
	type testCase struct {
		name    string
		src     spb.Selector
		ref     string
		attrs   map[string]string
		matches bool
		xErr    bool
	}

	cases := []testCase{
		{
			name:    "basic exact match",
			src:     spb.Selector{Identifier: "docker-image://docker.io/library/busybox:1.34.1-uclibc"},
			ref:     "docker-image://docker.io/library/busybox:1.34.1-uclibc",
			matches: true,
		},
		{
			name:    "docker-image scheme matches with only wildcard",
			src:     spb.Selector{Identifier: "*"},
			ref:     "docker-image://docker.io/library/busybox:latest",
			matches: true,
		},
		{
			name:    "docker-image scheme matches with wildcard",
			src:     spb.Selector{Identifier: "docker-image://docker.io/library/busybox:*"},
			ref:     "docker-image://docker.io/library/busybox:latest",
			matches: true,
		},
		{
			name:    "mis-matching scheme does not match",
			src:     spb.Selector{Identifier: "docker-image://docker.io/library/busybox:*"},
			ref:     "http://docker.io/library/busybox:latest",
			matches: false,
		},
		{
			name:    "http scheme matches with wildcard",
			src:     spb.Selector{Identifier: "http://docker.io/library/busybox:*"},
			ref:     "http://docker.io/library/busybox:latest",
			matches: true,
		},
		{
			name:    "http scheme matches https URL",
			src:     spb.Selector{Identifier: "https://docker.io/library/busybox:*"},
			ref:     "https://docker.io/library/busybox:latest",
			matches: true,
		},
		{
			name: "attr match with default constraint (equals) matches",
			src: spb.Selector{Identifier: "docker-image://docker.io/library/busybox:*",
				Constraints: []*spb.AttrConstraint{
					{
						Key:   "foo",
						Value: "bar",
						// Default equals
					},
				},
			},
			ref:     "docker-image://docker.io/library/busybox:latest",
			matches: true,
			attrs:   map[string]string{"foo": "bar"},
		},
		{
			name: "attr match with default constraint (equals) does not match",
			src: spb.Selector{Identifier: "docker-image://docker.io/library/busybox:*",
				Constraints: []*spb.AttrConstraint{
					{
						Key:   "foo",
						Value: "bar",
						// Default equals
					},
				},
			},
			ref:     "docker-image://docker.io/library/busybox:latest",
			matches: false,
			attrs:   map[string]string{"foo": "nope"},
		},
		{
			name: "attr match with explicit equals matches",
			src: spb.Selector{Identifier: "docker-image://docker.io/library/busybox:*",
				Constraints: []*spb.AttrConstraint{
					{
						Key:       "foo",
						Value:     "bar",
						Condition: spb.AttrMatch_EQUAL, // explicit equals
					},
				},
			},
			ref:     "docker-image://docker.io/library/busybox:latest",
			matches: true,
			attrs:   map[string]string{"foo": "bar"},
		},
		{
			name: "attr match with explicit equals does not match",
			src: spb.Selector{Identifier: "docker-image://docker.io/library/busybox:*",
				Constraints: []*spb.AttrConstraint{
					{
						Key:       "foo",
						Value:     "bar",
						Condition: spb.AttrMatch_EQUAL, // explicit equals
					},
				},
			},
			ref:     "docker-image://docker.io/library/busybox:latest",
			matches: false,
			attrs:   map[string]string{"foo": "nope"},
		},
		{
			name: "attr match not equal does not match",
			src: spb.Selector{Identifier: "docker-image://docker.io/library/busybox:*",
				Constraints: []*spb.AttrConstraint{
					{
						Key:       "foo",
						Value:     "bar",
						Condition: spb.AttrMatch_NOTEQUAL,
					},
				},
			},
			ref:     "docker-image://docker.io/library/busybox:latest",
			matches: false,
			attrs:   map[string]string{"foo": "bar"},
		},
		{
			name: "attr match not equal does match",
			src: spb.Selector{Identifier: "docker-image://docker.io/library/busybox:*",
				Constraints: []*spb.AttrConstraint{
					{
						Key:       "foo",
						Value:     "bar",
						Condition: spb.AttrMatch_NOTEQUAL,
					},
				},
			},
			ref:     "docker-image://docker.io/library/busybox:latest",
			matches: true,
			attrs:   map[string]string{"foo": "ok"},
		},
		{
			name: "matching attach match with simple strings",
			src: spb.Selector{Identifier: "docker-image://docker.io/library/busybox:*",
				Constraints: []*spb.AttrConstraint{
					{
						Key:       "foo",
						Value:     "bar",
						Condition: spb.AttrMatch_MATCHES,
					},
				},
			},
			ref:     "docker-image://docker.io/library/busybox:latest",
			matches: true,
			attrs:   map[string]string{"foo": "bar"},
		},
		{
			name: "non-matching attr match constraint simple strings",
			src: spb.Selector{Identifier: "docker-image://docker.io/library/busybox:*",
				Constraints: []*spb.AttrConstraint{
					{
						Key:       "foo",
						Value:     "asdf",
						Condition: spb.AttrMatch_MATCHES,
					},
				},
			},
			ref:     "docker-image://docker.io/library/busybox:latest",
			matches: false,
			attrs:   map[string]string{"foo": "bar"},
		},
		{
			name: "complex regex attr match",
			src: spb.Selector{Identifier: "docker-image://docker.io/library/busybox:*",
				Constraints: []*spb.AttrConstraint{
					{
						Key:       "foo",
						Value:     "^b\\w+",
						Condition: spb.AttrMatch_MATCHES,
					},
				},
			},
			ref:     "docker-image://docker.io/library/busybox:latest",
			matches: true,
			attrs:   map[string]string{"foo": "bar"},
		},
		{
			name: "attr constraint with non-matching regex",
			src: spb.Selector{Identifier: "docker-image://docker.io/library/busybox:*",
				Constraints: []*spb.AttrConstraint{
					{
						Key:       "foo",
						Value:     "^\\d+",
						Condition: spb.AttrMatch_MATCHES,
					},
				},
			},
			ref:     "docker-image://docker.io/library/busybox:latest",
			attrs:   map[string]string{"foo": "b1"},
			matches: false,
		},
		{
			name: "attr constraint with regex match",
			src: spb.Selector{Identifier: "docker-image://docker.io/library/busybox:*",
				Constraints: []*spb.AttrConstraint{
					{
						Key:       "foo",
						Value:     "\\d$",
						Condition: spb.AttrMatch_MATCHES,
					},
				},
			},
			ref:     "docker-image://docker.io/library/busybox:latest",
			attrs:   map[string]string{"foo": "b1"},
			matches: true,
		},
		{
			name: "unknown attr match condition type",
			src: spb.Selector{Identifier: "docker-image://docker.io/library/busybox:*",
				Constraints: []*spb.AttrConstraint{
					{
						Key:       "foo",
						Value:     "^b+",
						Condition: -1, // unknown condition
					},
				},
			},
			ref:     "docker-image://docker.io/library/busybox:latest",
			attrs:   map[string]string{"foo": "b1"},
			matches: false,
			xErr:    true,
		},
		{
			name: "matching constraint with extra attrs",
			src: spb.Selector{Identifier: "docker-image://docker.io/library/busybox:*",
				Constraints: []*spb.AttrConstraint{
					{
						Key:       "foo",
						Value:     "Foo",
						Condition: spb.AttrMatch_EQUAL,
					},
				},
			},
			ref:     "docker-image://docker.io/library/busybox:latest",
			attrs:   map[string]string{"foo": "Foo", "bar": "Bar"},
			matches: true,
		},
		{
			name: "multiple attrs with failed constraint",
			src: spb.Selector{Identifier: "docker-image://docker.io/library/busybox:*",
				Constraints: []*spb.AttrConstraint{
					{
						Key:       "foo",
						Value:     "Foo",
						Condition: spb.AttrMatch_EQUAL,
					},
					{
						Key:       "bar",
						Value:     "nope",
						Condition: spb.AttrMatch_EQUAL,
					},
				},
			},
			ref:     "docker-image://docker.io/library/busybox:latest",
			attrs:   map[string]string{"foo": "Foo", "bar": "Bar"},
			matches: false,
		},
		{
			name: "non-existent constraint key",
			src: spb.Selector{Identifier: "docker-image://docker.io/library/busybox:*",
				Constraints: []*spb.AttrConstraint{
					{
						Key:       "foo",
						Value:     "Foo",
						Condition: spb.AttrMatch_EQUAL,
					},
				},
			},
			ref:     "docker-image://docker.io/library/busybox:latest",
			matches: false,
		},
		{
			name: "non-existent constraint key w/ non-nill attr",
			src: spb.Selector{Identifier: "docker-image://docker.io/library/busybox:*",
				Constraints: []*spb.AttrConstraint{
					{
						Key:       "foo",
						Value:     "Foo",
						Condition: spb.AttrMatch_EQUAL,
					},
				},
			},
			ref:     "docker-image://docker.io/library/busybox:latest",
			attrs:   map[string]string{"bar": "Bar"},
			matches: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			matches, err := match(context.Background(), &selectorCache{Selector: &tc.src}, tc.ref, tc.attrs)
			if !tc.xErr {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			require.Equal(t, tc.matches, matches)
		})
	}
}
