package dagui_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/stretchr/testify/require"
)

func TestActivityIntervals(t *testing.T) {
	// Completed intervals only, no running interval.
	//
	// Timeline (not to scale):
	//
	//   04:13:00                                      04:19:58
	//        |                                             |
	//        [======]  [===================================]
	//           1m                    5m58s
	//
	// EarliestRunning: (zero)
	//
	// Expected Duration: 6m58s (sum of completed intervals)
	//
	t.Run("completed intervals only", func(t *testing.T) {
		var activity dagui.Activity
		err := json.Unmarshal([]byte(`{
			"CompletedIntervals": [
				{
					"Start": "2026-01-22T04:13:00Z",
					"End": "2026-01-22T04:14:00Z"
				},
				{
					"Start": "2026-01-22T04:15:00Z",
					"End": "2026-01-22T04:20:58Z"
				}
			],
			"EarliestRunning": "0001-01-01T00:00:00Z"
		}`), &activity)
		require.NoError(t, err)
		require.Equal(t, "6m58s", activity.Duration(time.Now()).String())
	})

	// EarliestRunning starts inside the last completed interval.
	// The running interval overlaps with the tail of the completed interval.
	//
	// Timeline (not to scale):
	//
	//   21:40:00       21:40:02                       21:42:19                         23:39:59 (now)
	//        |             |                              |                                 |
	//        [=]  [========|==============================]                                 |
	//        1s      ^               2m18s                                                  |
	//                |                                                                      |
	//                EarliestRunning                                                        |
	//                                                     |                                 |
	//                                                     [=================================]
	//                                                     ^-- running starts at latestEnd
	//                                                         (not EarliestRunning) to avoid
	//                                                         double-counting the overlap
	//
	// Expected Duration: 1h59m59s
	//   - Completed intervals: 1s + 2m18s = 2m19s (yielded fully, even though they overlap EarliestRunning)
	//   - Running interval: max(EarliestRunning, latestEnd) to now = 21:42:19 to 23:39:59 = 1h57m40s
	//   - Total: 2m19s + 1h57m40s = 1h59m59s
	//
	t.Run("running overlaps with completed interval", func(t *testing.T) {
		var activity dagui.Activity
		err := json.Unmarshal([]byte(`{
			"CompletedIntervals": [
				{
					"Start": "2026-01-26T21:40:00Z",
					"End": "2026-01-26T21:40:01Z"
				},
				{
					"Start": "2026-01-26T21:40:01Z",
					"End": "2026-01-26T21:42:19Z"
				}
			],
			"EarliestRunning": "2026-01-26T21:40:02Z"
		}`), &activity)
		require.NoError(t, err)

		now, err := time.Parse(time.RFC3339, "2026-01-26T23:39:59Z")
		require.NoError(t, err)
		require.Equal(t, "1h59m59s", activity.Duration(now).String())
	})

	// EarliestRunning starts inside an early completed interval, but there are
	// later completed intervals that start AFTER EarliestRunning.
	//
	// Timeline (not to scale):
	//
	//   21:40:00  21:40:02       21:42:14  21:42:15  21:42:18                          23:39:59 (now)
	//        |        |              |         |         |                                 |
	//        [=]  [===|==============]         [~]    [~~]                                 |
	//        1s    ^       2m13s               not yielded                                 |
	//              |                           (start > EarliestRunning)                   |
	//              EarliestRunning                                                         |
	//                                |                                                     |
	//                                [=====================================================]
	//                                ^-- running starts at max(EarliestRunning, latestEnd)
	//                                    = 21:42:14 (latestEnd of yielded intervals)
	//
	// Expected Duration: 1h59m59s
	//   - Completed intervals yielded: 1s + 2m13s = 2m14s (intervals starting <= EarliestRunning)
	//   - Later intervals (21:42:15-16, 21:42:18-19): not yielded (start > EarliestRunning)
	//   - Running interval: max(EarliestRunning, latestEnd) to now = 21:42:14 to 23:39:59 = 1h57m45s
	//   - Total: 2m14s + 1h57m45s = 1h59m59s
	//
	t.Run("running overlaps and later intervals exist", func(t *testing.T) {
		var activity dagui.Activity
		err := json.Unmarshal([]byte(`{
			"CompletedIntervals": [
				{
					"Start": "2026-01-26T21:40:00Z",
					"End": "2026-01-26T21:40:01Z"
				},
				{
					"Start": "2026-01-26T21:40:01Z",
					"End": "2026-01-26T21:42:14Z"
				},
				{
					"Start": "2026-01-26T21:42:15Z",
					"End": "2026-01-26T21:42:16Z"
				},
				{
					"Start": "2026-01-26T21:42:18Z",
					"End": "2026-01-26T21:42:19Z"
				}
			],
			"EarliestRunning": "2026-01-26T21:40:02Z"
		}`), &activity)
		require.NoError(t, err)

		now, err := time.Parse(time.RFC3339, "2026-01-26T23:39:59Z")
		require.NoError(t, err)
		require.Equal(t, "1h59m59s", activity.Duration(now).String())
	})
}
