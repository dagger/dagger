package scrub

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScrubbers(t *testing.T) {
	// quick sanity check to make sure the regexes work, since regexes are hard
	for _, s := range scrubs {
		require.Regexp(t, s.re, s.sample)
	}
}

func TestStabilizeDedupesAdjacentPortWarnings(t *testing.T) {
	input := strings.Join([]string{
		"│     ┃ 07:17:08 WRN port not ready host=abc.def.dagger.local error=\"dial tcp 10.88.0.76:6379: connect: connection refused\" elapsed=669.216µs",
		"│     ┃ 07:17:08 WRN port not ready host=abc.def.dagger.local error=\"dial tcp 10.88.0.76:6379: connect: connection refused\" elapsed=921.001µs",
		"│     ┃ 07:17:08 INF port is healthy host=abc.def.dagger.local endpoint=10.88.0.76:6379",
		"",
	}, "\n")

	expected := strings.Join([]string{
		"│     ┃ XX:XX:XX WRN port not ready host=xxxxxxxxxxxxx.xxxxxxxxxxxxx.dagger.local error=\"dial tcp 10.XX.XX.XX:6379: connect: connection refused\" elapsed=X.Xs",
		"│     ┃ XX:XX:XX INF port is healthy host=xxxxxxxxxxxxx.xxxxxxxxxxxxx.dagger.local endpoint=10.XX.XX.XX:6379",
		"",
	}, "\n")

	require.Equal(t, expected, Stabilize(input))
}

func TestStabilizeRemovesPacketLoss(t *testing.T) {
	input := "✘ .failEffect: Container! 3.7s ERROR (5.88% dropped)\n"
	expected := "✘ .failEffect: Container! X.Xs ERROR\n"

	require.Equal(t, expected, Stabilize(input))
}

func TestStabilizeRemovesNetworkMetrics(t *testing.T) {
	input := strings.Join([]string{
		"✔ aggregate 1.2s ◆ Network Rx: 770 B ◆ Network Tx: 1.2 kB",
		"✔ scoped 2.3s ◆ External Rx: 2 MB ◆ Internal Tx: 3 B",
		"✔ dropped 3.4s ◆ Network Rx: 4.5 kB (5.88% dropped)",
		"",
	}, "\n")
	expected := strings.Join([]string{
		"✔ aggregate X.Xs",
		"✔ scoped X.Xs",
		"✔ dropped X.Xs",
		"",
	}, "\n")

	require.Equal(t, expected, Stabilize(input))
}
