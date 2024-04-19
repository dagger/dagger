package resources

import (
	"os"
	"path/filepath"
	"testing"

	resourcestypes "github.com/moby/buildkit/executor/resources/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePidsStat(t *testing.T) {
	testDir := t.TempDir()

	err := os.WriteFile(filepath.Join(testDir, "pids.current"), []byte("123"), 0644)
	assert.NoError(t, err)

	expectedPidsStat := &resourcestypes.PIDsStat{
		Current: uint64Ptr(123),
	}
	stats, err := getCgroupPIDsStat(filepath.Join(testDir))
	require.NoError(t, err)
	assert.Equal(t, expectedPidsStat, stats)
}
