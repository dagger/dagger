package event

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEvent(t *testing.T) {
	action := ActionTransition{
		Name:  "test",
		State: ActionStateRunning,
	}
	event := New(action)

	require.NoError(t, event.Validate())
}
