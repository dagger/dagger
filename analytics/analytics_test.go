package analytics

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// The first upload must overlap the command, while Close must still wait for
// that upload and deliver events captured while it was in flight.
func TestFirstEventStartsUploadBeforeClose(t *testing.T) {
	requests := make(chan []Event, 4)
	release := make(chan struct{})
	original := http.DefaultClient
	http.DefaultClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var events []Event
		dec := json.NewDecoder(r.Body)
		for {
			var event Event
			if err := dec.Decode(&event); err == io.EOF {
				break
			} else if err != nil {
				return nil, err
			}
			events = append(events, event)
		}
		requests <- events
		<-release
		return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})}
	t.Cleanup(func() { http.DefaultClient = original })
	tracker := New(Config{})
	t.Cleanup(func() {
		_ = tracker.Close()
		<-tracker.(*CloudTracker).doneCh
	})
	t.Cleanup(func() { close(release) })

	tracker.Capture(t.Context(), "cli_command", map[string]string{"name": "check"})
	select {
	case batch := <-requests:
		require.Len(t, batch, 1)
		require.Equal(t, "cli_command", batch[0].Type)
		require.Equal(t, "check", batch[0].Properties["name"])
	case <-time.After(flushInterval / 2):
		t.Fatal("first event waited for the periodic flush")
	}

	tracker.Capture(t.Context(), "module_call", nil)
	closed := make(chan error, 1)
	go func() { closed <- tracker.Close() }()
	select {
	case <-closed:
		t.Fatal("Close returned before the in-flight upload completed")
	case <-time.After(10 * time.Millisecond):
	}
	release <- struct{}{}
	select {
	case batch := <-requests:
		require.Len(t, batch, 1)
		require.Equal(t, "module_call", batch[0].Type)
	case <-time.After(time.Second):
		t.Fatal("Close did not drain the remaining event")
	}
	release <- struct{}{}
	require.NoError(t, <-closed)
	tracker.Capture(t.Context(), "after_close", nil)
	require.NoError(t, tracker.Close())
	require.Empty(t, requests)
}

func TestTrackerCloseWithoutEvents(t *testing.T) {
	require.NoError(t, New(Config{}).Close())
}

func TestDoNotTrack(t *testing.T) {
	tracker := New(Config{DoNotTrack: true})
	tracker.Capture(t.Context(), "cli_command", nil)
	require.IsType(t, &noopTracker{}, tracker)
	require.NoError(t, tracker.Close())
}
