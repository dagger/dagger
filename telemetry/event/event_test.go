package event

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEvent(t *testing.T) {
	require.ErrorIs(t, New(ActionStarted{}).Validate(), ErrMalformedEvent)

	require.NoError(t, New(ActionStarted{
		Name: "myaction",
	}).Validate())
}
