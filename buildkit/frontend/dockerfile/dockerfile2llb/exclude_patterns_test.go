//go:build dfexcludepatterns
// +build dfexcludepatterns

package dockerfile2llb

import (
	"testing"

	"github.com/moby/buildkit/util/appcontext"
	"github.com/stretchr/testify/assert"
)

func TestDockerfileCopyExcludePatterns(t *testing.T) {
	df := `FROM scratch
COPY --exclude=src/*.go --exclude=tmp/*.txt dir /sub/
`
	_, _, _, _, err := Dockerfile2LLB(appcontext.Context(), []byte(df), ConvertOpt{})
	assert.NoError(t, err)
}

func TestDockerfileAddExcludePatterns(t *testing.T) {
	df := `FROM scratch
ADD --exclude=src/*.go --exclude=tmp/*.txt dir /sub/
`
	_, _, _, _, err := Dockerfile2LLB(appcontext.Context(), []byte(df), ConvertOpt{})
	assert.NoError(t, err)
}
