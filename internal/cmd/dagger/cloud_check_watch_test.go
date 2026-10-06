package daggercmd

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func groupedRow(name, result string) groupedCloudListRow {
	return groupedCloudListRow{
		Values:    map[string]string{"check": name},
		Result:    result,
		UpdatedAt: time.Now(),
	}
}

func groupedRowStatus(name, result, status string) groupedCloudListRow {
	row := groupedRow(name, result)
	row.Status = status
	return row
}

func TestPrintCloudCheckWatchProgress(t *testing.T) {
	var buf bytes.Buffer
	printCloudCheckWatchProgress(&buf, []groupedCloudListRow{
		groupedRowStatus("lint", "green", "success"),
		groupedRowStatus("test", "pending", "running"),
		groupedRowStatus("build", "pending", "queued"),
		groupedRowStatus("deploy", "red", "errored"),
	})
	out := buf.String()
	require.Contains(t, out, "pending: test")
	require.Contains(t, out, "queued: build")
	require.Contains(t, out, "errored: deploy")
	// finished checks are not reported as in-progress.
	require.NotContains(t, out, "lint")
}

func TestPrintCloudCheckWatchProgressAllDone(t *testing.T) {
	var buf bytes.Buffer
	printCloudCheckWatchProgress(&buf, []groupedCloudListRow{groupedRow("lint", "green")})
	require.Empty(t, buf.String())
}

func TestSummarizeCloudCheckResults(t *testing.T) {
	for _, tc := range []struct {
		name        string
		rows        []groupedCloudListRow
		wantPending bool
		wantFailed  bool
	}{
		{name: "empty"},
		{name: "all green", rows: []groupedCloudListRow{groupedRow("a", "green"), groupedRow("b", "green")}},
		{name: "pending", rows: []groupedCloudListRow{groupedRow("a", "green"), groupedRow("b", "pending")}, wantPending: true},
		{name: "failed", rows: []groupedCloudListRow{groupedRow("a", "green"), groupedRow("b", "red")}, wantFailed: true},
		{name: "pending and failed", rows: []groupedCloudListRow{groupedRow("a", "pending"), groupedRow("b", "red")}, wantPending: true, wantFailed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pending, failed := summarizeCloudCheckResults(tc.rows)
			require.Equal(t, tc.wantPending, pending)
			require.Equal(t, tc.wantFailed, failed)
		})
	}
}

func TestCloudChecksFailedError(t *testing.T) {
	err := cloudChecksFailedError([]groupedCloudListRow{
		groupedRow("lint", "green"),
		groupedRow("test", "red"),
		groupedRow("build", "pending"),
	})
	require.ErrorContains(t, err, "Cloud checks did not succeed")
	require.ErrorContains(t, err, "test")
	require.NotContains(t, err.Error(), "lint")
}

func TestRunCloudCheckListFailFastRequiresWatch(t *testing.T) {
	oldWatch, oldFailFast := cloudCheckListWatch, cloudCheckListFailFast
	cloudCheckListWatch, cloudCheckListFailFast = false, true
	t.Cleanup(func() { cloudCheckListWatch, cloudCheckListFailFast = oldWatch, oldFailFast })

	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	err := runCloudCheckList(cmd, nil)
	require.ErrorContains(t, err, "--fail-fast requires --watch")
}

func newWatchTestCmd(t *testing.T) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	return cmd, &out
}

// fetchSequence returns a fetch func that yields each state in order, repeating
// the final state once exhausted.
func fetchSequence(states ...[]groupedCloudListRow) func(context.Context) ([]groupedCloudListRow, error) {
	var i int
	return func(context.Context) ([]groupedCloudListRow, error) {
		state := states[i]
		if i < len(states)-1 {
			i++
		}
		return state, nil
	}
}

func TestWatchCloudCheckListWaitsUntilFinishedSuccess(t *testing.T) {
	oldFailed, oldFailFast := cloudCheckListFailed, cloudCheckListFailFast
	cloudCheckListFailed, cloudCheckListFailFast = false, false
	t.Cleanup(func() { cloudCheckListFailed, cloudCheckListFailFast = oldFailed, oldFailFast })

	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	fetch := fetchSequence(
		[]groupedCloudListRow{groupedRowStatus("lint", "pending", "queued"), groupedRowStatus("test", "pending", "running")},
		[]groupedCloudListRow{groupedRow("lint", "green"), groupedRowStatus("test", "pending", "running")},
		[]groupedCloudListRow{groupedRow("lint", "green"), groupedRow("test", "green")},
	)
	err := watchCloudCheckListLoop(cmd, "github.com/example/project", time.Millisecond, fetch)
	require.NoError(t, err)
	require.Contains(t, out.String(), "lint")
	require.Contains(t, out.String(), "test")
	// progress feedback is emitted to stderr while waiting.
	require.Contains(t, errOut.String(), "queued: lint")
	require.Contains(t, errOut.String(), "pending: test")
}

func TestWatchCloudCheckListWaitsUntilFinishedFailure(t *testing.T) {
	oldFailed, oldFailFast := cloudCheckListFailed, cloudCheckListFailFast
	cloudCheckListFailed, cloudCheckListFailFast = false, false
	t.Cleanup(func() { cloudCheckListFailed, cloudCheckListFailFast = oldFailed, oldFailFast })

	cmd, _ := newWatchTestCmd(t)
	fetch := fetchSequence(
		[]groupedCloudListRow{groupedRow("lint", "pending"), groupedRow("test", "red")},
		[]groupedCloudListRow{groupedRow("lint", "green"), groupedRow("test", "red")},
	)
	err := watchCloudCheckListLoop(cmd, "github.com/example/project", time.Millisecond, fetch)
	require.ErrorContains(t, err, "Cloud checks did not succeed")
	require.ErrorContains(t, err, "test")
}

func TestWatchCloudCheckListFailFastStopsEarly(t *testing.T) {
	oldFailed, oldFailFast := cloudCheckListFailed, cloudCheckListFailFast
	cloudCheckListFailed, cloudCheckListFailFast = false, true
	t.Cleanup(func() { cloudCheckListFailed, cloudCheckListFailFast = oldFailed, oldFailFast })

	var calls int
	fetch := func(context.Context) ([]groupedCloudListRow, error) {
		calls++
		// test already failed while lint is still pending.
		return []groupedCloudListRow{groupedRow("lint", "pending"), groupedRow("test", "red")}, nil
	}
	cmd, _ := newWatchTestCmd(t)
	err := watchCloudCheckListLoop(cmd, "github.com/example/project", time.Millisecond, fetch)
	require.ErrorContains(t, err, "Cloud checks did not succeed")
	// fail-fast must not keep polling while a check is still pending.
	require.Equal(t, 1, calls)
}

func TestWatchCloudCheckListCancelledContext(t *testing.T) {
	oldFailed, oldFailFast := cloudCheckListFailed, cloudCheckListFailFast
	cloudCheckListFailed, cloudCheckListFailFast = false, false
	t.Cleanup(func() { cloudCheckListFailed, cloudCheckListFailFast = oldFailed, oldFailFast })

	ctx, cancel := context.WithCancel(context.Background())
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)
	var out bytes.Buffer
	cmd.SetOut(&out)

	var calls int
	fetch := func(context.Context) ([]groupedCloudListRow, error) {
		calls++
		if calls == 1 {
			cancel()
		}
		return []groupedCloudListRow{groupedRow("lint", "pending")}, nil
	}
	err := watchCloudCheckListLoop(cmd, "github.com/example/project", time.Hour, fetch)
	require.ErrorIs(t, err, context.Canceled)
}
