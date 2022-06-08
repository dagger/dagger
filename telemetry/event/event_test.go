package event

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEvent(t *testing.T) {
	action := ActionTransitioned{
		Name:  "test",
		State: ActionStateRunning,
	}
	event := New(action)

	require.NoError(t, event.Validate())
}
