package telemetry

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestDefaultLogFile(t *testing.T) {
	lf := New().logFile()
	lfs := strings.Split(lf, ".")
	prefix, id, ext := lfs[0], lfs[1], lfs[2]
	assert.Equal(t, "telemetry", prefix, fmt.Sprintf("log file: %#v", lf))
	assert.NotPanicsf(t, func() { uuid.MustParse(id) }, fmt.Sprintf("log file: %#v", lf))
	assert.Equal(t, "log", ext, fmt.Sprintf("log file: %#v", lf))
}
