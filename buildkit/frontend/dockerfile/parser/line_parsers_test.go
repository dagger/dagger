package parser

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
)

func TestParseNameValOldFormat(t *testing.T) {
	directive := directives{}
	node, err := parseNameVal("foo bar", "LABEL", &directive)
	require.NoError(t, err)

	expected := &Node{
		Value: "foo",
		Next:  &Node{Value: "bar"},
	}
	require.Equal(t, expected, node, cmpNodeOpt)
}

var cmpNodeOpt = cmp.AllowUnexported(Node{})

func TestParseNameValNewFormat(t *testing.T) {
	directive := directives{}
	node, err := parseNameVal("foo=bar thing=star", "LABEL", &directive)
	require.NoError(t, err)

	expected := &Node{
		Value: "foo",
		Next: &Node{
			Value: "bar",
			Next: &Node{
				Value: "thing",
				Next: &Node{
					Value: "star",
				},
			},
		},
	}
	require.Equal(t, expected, node, cmpNodeOpt)
}

func TestParseNameValWithoutVal(t *testing.T) {
	directive := directives{}
	// In Config.Env, a variable without `=` is removed from the environment. (#31634)
	// However, in Dockerfile, we don't allow "unsetting" an environment variable. (#11922)
	_, err := parseNameVal("foo", "ENV", &directive)
	require.Error(t, err, "ENV must have two arguments")
}
