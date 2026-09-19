package dangv2

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vito/dang/v2/pkg/dang"

	dangshared "github.com/dagger/dagger/core/sdk/dang/shared"
)

// inferReport builds the kind of error dang.RunDir returns for a module with
// inference errors: an InferenceErrors of source-located errors whose
// rendering carries multi-line, ANSI-colored source excerpts.
func inferReport(inner error) *dang.InferenceErrors {
	return &dang.InferenceErrors{
		Errors: []error{
			dang.NewSourceError(inner,
				&dang.SourceLocation{Filename: "main.dang", Line: 2, Column: 3, Length: 4},
				"type Foo {\n  bogus: Int! { 1 }\n}\n"),
		},
	}
}

func TestReportDangSourceError(t *testing.T) {
	t.Parallel()

	report := inferReport(errors.New("unknown type Bogus"))
	require.True(t, isDangSourceError(report))

	var stderr bytes.Buffer
	err := reportDangSourceError(&stderr, report)

	// The rendered report lands on stderr verbatim, newline-terminated
	// exactly once.
	require.Equal(t, strings.TrimRight(report.Error(), "\n")+"\n", stderr.String())
	require.Contains(t, stderr.String(), "unknown type Bogus")
	require.Contains(t, stderr.String(), "main.dang:2:3")
	require.Contains(t, stderr.String(), "\033[") // Dang's ANSI styling is preserved

	// The span error is short and carries none of the report.
	require.Equal(t, "Dang module failed to load; see logs", err.Error())
	require.NotContains(t, err.Error(), "main.dang")

	// The original is still reachable for errors.As-based handling.
	var inferErrs *dang.InferenceErrors
	require.ErrorAs(t, err, &inferErrs)
	require.Same(t, report, inferErrs)
}

// TestDangSourceErrorKeepsGraphQLExtraction locks in that ConvertError still
// sees a GraphQL error raised while evaluating a module's top-level source
// through the short load error, so its message and extensions survive.
func TestDangSourceErrorKeepsGraphQLExtraction(t *testing.T) {
	t.Parallel()

	gqlErr := &gqlerror.Error{
		Message:    "boom",
		Extensions: map[string]any{"exitCode": 2},
	}
	evalErr := dang.NewSourceError(gqlErr,
		&dang.SourceLocation{Filename: "main.dang", Line: 1, Column: 1, Length: 1},
		"container.from(\"nope\").sync\n")
	require.True(t, isDangSourceError(evalErr))

	err := reportDangSourceError(&bytes.Buffer{}, evalErr)
	converted := dangshared.ConvertError(err)
	require.Equal(t, "boom", converted.Message)
	require.NotContains(t, converted.Message, "main.dang")
}

func TestIsDangSourceErrorIgnoresInfrastructureErrors(t *testing.T) {
	t.Parallel()

	require.False(t, isDangSourceError(errors.New("no .dang files found in directory: /src")))
	require.False(t, isDangSourceError(fmt.Errorf("read module entrypoint directory: %w", errors.New("permission denied"))))
}
