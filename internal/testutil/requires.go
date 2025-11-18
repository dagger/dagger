package testutil

import (
	"fmt"
	"strings"

	"github.com/stretchr/testify/require"
)

// HasPrefix tests that s starts with expectedPrefix
func HasPrefix(t require.TestingT, expectedPrefix, s string, msgAndArgs ...interface{}) {
	if strings.HasPrefix(s, expectedPrefix) {
		return
	}
	require.Fail(t, fmt.Sprintf("Missing prefix: \n"+
		"expected : %s\n"+
		"in string: %s", expectedPrefix, s), msgAndArgs...)
}
