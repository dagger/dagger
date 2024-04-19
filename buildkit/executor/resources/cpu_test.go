package resources

import (
	"os"
	"path/filepath"
	"testing"

	resourcestypes "github.com/moby/buildkit/executor/resources/types"
	"github.com/stretchr/testify/require"
)

func createDummyCgroupFS(t *testing.T, cpuStatContents string) (string, error) {
	tmpDir := t.TempDir()

	err := os.WriteFile(filepath.Join(tmpDir, "cpu.stat"), []byte(cpuStatContents), 0644)
	if err != nil {
		return "", err
	}

	return tmpDir, nil
}

func TestGetCgroupCPUStat(t *testing.T) {
	cpuStatContents := `usage_usec 1234567
user_usec 123456
system_usec 123456
nr_periods 123
nr_throttled 12
throttled_usec 123456`

	tmpDir, err := createDummyCgroupFS(t, cpuStatContents)
	require.NoError(t, err)

	cpuStat, err := getCgroupCPUStat(tmpDir)
	require.NoError(t, err)

	require.NotNil(t, cpuStat.UsageNanos)
	require.Equal(t, uint64(1234567000), *cpuStat.UsageNanos)

	require.NotNil(t, cpuStat.UserNanos)
	require.Equal(t, uint64(123456000), *cpuStat.UserNanos)

	require.NotNil(t, cpuStat.SystemNanos)
	require.Equal(t, uint64(123456000), *cpuStat.SystemNanos)

	require.NotNil(t, cpuStat.NrPeriods)
	require.Equal(t, uint32(123), *cpuStat.NrPeriods)

	require.NotNil(t, cpuStat.NrThrottled)
	require.Equal(t, uint32(12), *cpuStat.NrThrottled)

	require.NotNil(t, cpuStat.ThrottledNanos)
	require.Equal(t, uint64(123456000), *cpuStat.ThrottledNanos)
}
func TestReadPressureFile(t *testing.T) {
	pressureContents := `some avg10=1.23 avg60=4.56 avg300=7.89 total=3031
full avg10=0.12 avg60=0.34 avg300=0.56 total=9876`

	tmpFile := filepath.Join(t.TempDir(), "pressure_test")
	err := os.WriteFile(tmpFile, []byte(pressureContents), os.ModePerm)
	require.NoError(t, err)

	pressure, err := parsePressureFile(tmpFile)
	require.NoError(t, err)

	some123 := 1.23
	some456 := 4.56
	some789 := 7.89
	some3031 := uint64(3031)
	full12 := 0.12
	full34 := 0.34
	full56 := 0.56
	full9876 := uint64(9876)

	expected := &resourcestypes.Pressure{
		Some: &resourcestypes.PressureValues{
			Avg10:  &some123,
			Avg60:  &some456,
			Avg300: &some789,
			Total:  &some3031,
		},
		Full: &resourcestypes.PressureValues{
			Avg10:  &full12,
			Avg60:  &full34,
			Avg300: &full56,
			Total:  &full9876,
		},
	}

	require.Equal(t, expected, pressure)
}
