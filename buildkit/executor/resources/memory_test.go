package resources

import (
	"os"
	"path/filepath"
	"testing"

	resourcestypes "github.com/moby/buildkit/executor/resources/types"
	"github.com/stretchr/testify/assert"
)

func TestParseMemoryStat(t *testing.T) {
	testDir := t.TempDir()

	memoryStatContents := `anon 24576
file 12791808
kernel_stack 8192
pagetables 4096
sock 2048
shmem 16384
file_mapped 8192
file_dirty 32768
file_writeback 16384
slab 1503104
pgscan 100
pgsteal 99
pgfault 32711
pgmajfault 12`
	err := os.WriteFile(filepath.Join(testDir, memoryStatFile), []byte(memoryStatContents), 0644)
	assert.NoError(t, err)

	memoryPressureContents := `some avg10=1.23 avg60=4.56 avg300=7.89 total=3031
full avg10=0.12 avg60=0.34 avg300=0.56 total=9876`
	err = os.WriteFile(filepath.Join(testDir, memoryPressureFile), []byte(memoryPressureContents), 0644)
	assert.NoError(t, err)

	memoryEventsContents := `low 4
high 3
max 2
oom 1
oom_kill 5`
	err = os.WriteFile(filepath.Join(testDir, memoryEventsFile), []byte(memoryEventsContents), 0644)
	assert.NoError(t, err)

	err = os.WriteFile(filepath.Join(testDir, memoryPeakFile), []byte("123456"), 0644)
	assert.NoError(t, err)

	err = os.WriteFile(filepath.Join(testDir, memorySwapCurrentFile), []byte("987654"), 0644)
	assert.NoError(t, err)

	memoryStat, err := getCgroupMemoryStat(testDir)
	assert.NoError(t, err)

	var expectedPressure = &resourcestypes.Pressure{
		Some: &resourcestypes.PressureValues{
			Avg10:  float64Ptr(1.23),
			Avg60:  float64Ptr(4.56),
			Avg300: float64Ptr(7.89),
			Total:  uint64Ptr(3031),
		},
		Full: &resourcestypes.PressureValues{
			Avg10:  float64Ptr(0.12),
			Avg60:  float64Ptr(0.34),
			Avg300: float64Ptr(0.56),
			Total:  uint64Ptr(9876),
		},
	}

	expectedMemoryStat := &resourcestypes.MemoryStat{
		SwapBytes:     uint64Ptr(987654),
		Anon:          uint64Ptr(24576),
		File:          uint64Ptr(12791808),
		KernelStack:   uint64Ptr(8192),
		PageTables:    uint64Ptr(4096),
		Sock:          uint64Ptr(2048),
		Shmem:         uint64Ptr(16384),
		FileMapped:    uint64Ptr(8192),
		FileDirty:     uint64Ptr(32768),
		FileWriteback: uint64Ptr(16384),
		Slab:          uint64Ptr(1503104),
		Pgscan:        uint64Ptr(100),
		Pgsteal:       uint64Ptr(99),
		Pgfault:       uint64Ptr(32711),
		Pgmajfault:    uint64Ptr(12),
		Peak:          uint64Ptr(123456),
		LowEvents:     4,
		HighEvents:    3,
		MaxEvents:     2,
		OomEvents:     1,
		OomKillEvents: 5,
		Pressure:      expectedPressure,
	}
	assert.Equal(t, expectedMemoryStat, memoryStat)
}

func float64Ptr(v float64) *float64 {
	return &v
}
