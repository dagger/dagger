package daggercmd

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	cloudapi "github.com/dagger/dagger/internal/cloud"
)

type fakeTrialAPI struct {
	status           string
	trialEnd         *string
	hasPaymentMethod bool
	err              error
}

func (f *fakeTrialAPI) OrgPaymentStatus(ctx context.Context, org string) (*cloudapi.PaymentStatus, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &cloudapi.PaymentStatus{Status: f.status, TrialEnd: f.trialEnd, HasPaymentMethod: f.hasPaymentMethod}, nil
}

func TestCheckTrialStatus(t *testing.T) {
	ctx := context.Background()
	end := "2026-09-30T12:00:00Z"

	t.Run("trial without a payment method", func(t *testing.T) {
		status, err := checkTrialStatus(ctx, &fakeTrialAPI{status: "in_trial", trialEnd: &end}, "acme")
		require.NoError(t, err)
		require.True(t, status.NeedsPayment)
		require.NotNil(t, status.TrialEnd)
		require.Equal(t, end, status.TrialEnd.Format(time.RFC3339))
	})

	t.Run("trial with a payment method", func(t *testing.T) {
		status, err := checkTrialStatus(ctx, &fakeTrialAPI{status: "in_trial", hasPaymentMethod: true}, "acme")
		require.NoError(t, err)
		require.False(t, status.NeedsPayment)
	})

	t.Run("paid orgs are not reminded", func(t *testing.T) {
		status, err := checkTrialStatus(ctx, &fakeTrialAPI{status: "active"}, "acme")
		require.NoError(t, err)
		require.False(t, status.NeedsPayment)
	})

	t.Run("a failed query is an error, not a reminder", func(t *testing.T) {
		_, err := checkTrialStatus(ctx, &fakeTrialAPI{err: errors.New("Cannot query field")}, "acme")
		require.Error(t, err)
	})
}

func TestTrialReminder(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { t := now.Add(d); return &t }

	for _, tc := range []struct {
		end  *time.Time
		want string
	}{
		{nil, "You are on a free Dagger Cloud trial for acme."},
		{at(4*24*time.Hour + time.Hour), "You are on a free Dagger Cloud trial for acme, ending in 5 days."},
		{at(20 * time.Hour), "You are on a free Dagger Cloud trial for acme, ending in 1 day."},
		{at(-time.Hour), "You are on a free Dagger Cloud trial for acme, ending today."},
	} {
		require.Equal(t, tc.want, trialReminderHeadline("acme", tc.end, now))
	}

	var buf bytes.Buffer
	trialReminder(&buf, "acme", at(48*time.Hour), now)
	out := buf.String()
	require.Contains(t, out, "ending in 2 days.")
	require.Contains(t, out, `Enter payment details with "dagger cloud billing" to avoid service disruption once your trial ends.`)
	require.Contains(t, out, "To hide set DAGGER_NO_NAG=1")
}

func TestSkipNag(t *testing.T) {
	for _, env := range []string{"DAGGER_NO_NAG", "NOTHANKS", "SHUTUP", "GOAWAY", "STOPIT"} {
		t.Setenv(env, "")
	}
	require.False(t, skipNag())
	t.Setenv("DAGGER_NO_NAG", "1")
	require.True(t, skipNag())
}
