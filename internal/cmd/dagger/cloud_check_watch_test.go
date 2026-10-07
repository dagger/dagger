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

func TestReportCloudCheckWatchProgressInitialState(t *testing.T) {
	var buf bytes.Buffer
	rows := []groupedCloudListRow{
		groupedRowStatus("lint", "green", "success"),
		groupedRowStatus("test", "pending", "running"),
		groupedRowStatus("build", "pending", "queued"),
	}
	state := reportCloudCheckWatchProgress(&buf, nil, true, rows)
	out := buf.String()
	// first poll lists the full initial state of every check.
	require.Contains(t, out, "Watching 3 Cloud checks:")
	require.Contains(t, out, "lint: success")
	require.Contains(t, out, "test: running")
	require.Contains(t, out, "build: queued")
	require.Equal(t, "success", state["lint"])
	require.Equal(t, "running", state["test"])
	require.Equal(t, "queued", state["build"])
}

func TestReportCloudCheckWatchProgressOnlyChanges(t *testing.T) {
	prev := map[string]string{"lint": "running", "test": "queued", "build": "running"}
	var buf bytes.Buffer
	rows := []groupedCloudListRow{
		groupedRowStatus("lint", "green", "success"),  // changed
		groupedRowStatus("test", "pending", "queued"), // unchanged
		groupedRowStatus("build", "red", "errored"),   // changed
	}
	state := reportCloudCheckWatchProgress(&buf, prev, false, rows)
	out := buf.String()
	require.Contains(t, out, "lint: running → success")
	require.Contains(t, out, "build: running → errored")
	// unchanged checks are not reported.
	require.NotContains(t, out, "test:")
	require.Equal(t, "errored", state["build"])
}

func TestReportCloudCheckWatchProgressNoChanges(t *testing.T) {
	prev := map[string]string{"lint": "running"}
	var buf bytes.Buffer
	reportCloudCheckWatchProgress(&buf, prev, false, []groupedCloudListRow{groupedRowStatus("lint", "pending", "running")})
	require.Empty(t, buf.String())
}

func TestReportCloudCheckWatchProgressNewCheck(t *testing.T) {
	prev := map[string]string{"lint": "running"}
	var buf bytes.Buffer
	reportCloudCheckWatchProgress(&buf, prev, false, []groupedCloudListRow{
		groupedRowStatus("lint", "pending", "running"),
		groupedRowStatus("test", "pending", "queued"),
	})
	out := buf.String()
	// a newly-appeared check is reported with just its current state.
	require.Contains(t, out, "test: queued")
	require.NotContains(t, out, "lint:")
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
	require.ErrorContains(t, err, "cloud checks did not succeed")
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

func newWatchTestCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	return cmd
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
		[]groupedCloudListRow{groupedRowStatus("lint", "green", "success"), groupedRowStatus("test", "pending", "running")},
		[]groupedCloudListRow{groupedRowStatus("lint", "green", "success"), groupedRowStatus("test", "green", "success")},
	)
	err := watchCloudCheckListLoop(cmd, "github.com/example/project", time.Millisecond, fetch)
	require.NoError(t, err)
	require.Contains(t, out.String(), "lint")
	require.Contains(t, out.String(), "test")
	progress := errOut.String()
	// first poll shows the full initial state.
	require.Contains(t, progress, "Watching 2 Cloud checks:")
	require.Contains(t, progress, "lint: queued")
	require.Contains(t, progress, "test: running")
	// subsequent polls show only transitions.
	require.Contains(t, progress, "lint: queued → success")
	require.Contains(t, progress, "test: running → success")
}

func TestWatchCloudCheckListWaitsUntilFinishedFailure(t *testing.T) {
	oldFailed, oldFailFast := cloudCheckListFailed, cloudCheckListFailFast
	cloudCheckListFailed, cloudCheckListFailFast = false, false
	t.Cleanup(func() { cloudCheckListFailed, cloudCheckListFailFast = oldFailed, oldFailFast })

	cmd := newWatchTestCmd(t)
	fetch := fetchSequence(
		[]groupedCloudListRow{groupedRow("lint", "pending"), groupedRow("test", "red")},
		[]groupedCloudListRow{groupedRow("lint", "green"), groupedRow("test", "red")},
	)
	err := watchCloudCheckListLoop(cmd, "github.com/example/project", time.Millisecond, fetch)
	require.ErrorContains(t, err, "cloud checks did not succeed")
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
	cmd := newWatchTestCmd(t)
	err := watchCloudCheckListLoop(cmd, "github.com/example/project", time.Millisecond, fetch)
	require.ErrorContains(t, err, "cloud checks did not succeed")
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
