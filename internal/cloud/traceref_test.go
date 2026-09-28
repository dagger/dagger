package cloud

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseTraceRef(t *testing.T) {
	const id = "2f81064627bbd17b45441b93ac4fc8cf"
	for _, s := range []string{
		id,
		strings.ToUpper(id),
		" dagger trace " + id + "\n",
		"dagger cloud traces view " + id,
		"https://dagger.cloud/org/traces/" + id,
		"https://dagger.cloud/org/traces/" + id + "/spans/123?focus=true",
	} {
		ref, err := ParseTraceRef(s)
		require.NoError(t, err, s)
		require.Equal(t, id, ref.TraceID)
	}

	ref, err := ParseTraceRef("https://dagger.cloud/acme/traces/" + id + "?span=0102030405060708")
	require.NoError(t, err)
	require.Equal(t, TraceRef{TraceID: id, Org: "acme", SpanID: "0102030405060708"}, ref)

	for _, s := range []string{
		"",
		"00000000000000000000000000000000",
		"bad",
		"dagger trace " + id + "; echo secret",
		"https://evil.example/org/traces/" + id,
		"https://dagger.cloud@evil.example/traces/" + id,
		"http://dagger.cloud/org/traces/" + id,
	} {
		_, err := ParseTraceRef(s)
		require.Error(t, err, s)
		require.NotContains(t, err.Error(), s+";")
	}
}
