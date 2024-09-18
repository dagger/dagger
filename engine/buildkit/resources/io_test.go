package resources

import (
	"os"
	"path/filepath"
	"testing"

	resourcestypes "github.com/moby/buildkit/executor/resources/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseIOStat(t *testing.T) {
	testDir := t.TempDir()

	ioStatContents := `8:0 rbytes=1024 wbytes=2048 dbytes=4096 rios=16 wios=32 dios=64
8:1 rbytes=512 wbytes=1024 dbytes=2048 rios=8 wios=16 dios=32`
	err := os.WriteFile(filepath.Join(testDir, "io.stat"), []byte(ioStatContents), 0644)
	require.NoError(t, err)

	ioPressureContents := `some avg10=1.23 avg60=4.56 avg300=7.89 total=3031
full avg10=0.12 avg60=0.34 avg300=0.56 total=9876`
	err = os.WriteFile(filepath.Join(testDir, "io.pressure"), []byte(ioPressureContents), 0644)
	require.NoError(t, err)

	ioStat, err := getCgroupIOStat(testDir)
	require.NoError(t, err)

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

	expectedIOStat := &resourcestypes.IOStat{
		ReadBytes:    uint64Ptr(1024 + 512),
		WriteBytes:   uint64Ptr(2048 + 1024),
		DiscardBytes: uint64Ptr(4096 + 2048),
		ReadIOs:      uint64Ptr(16 + 8),
		WriteIOs:     uint64Ptr(32 + 16),
		DiscardIOs:   uint64Ptr(64 + 32),
		Pressure:     expectedPressure,
	}
	assert.Equal(t, expectedIOStat, ioStat)
}
