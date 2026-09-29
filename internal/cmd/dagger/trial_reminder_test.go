package daggercmd

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adrg/xdg"
	"github.com/stretchr/testify/require"

	cloudapi "github.com/dagger/dagger/internal/cloud"
)

type fakeTrialAPI struct {
	status           string
	trialEnd         *string
	hasPaymentMethod bool
	paymentErr       error

	detailsCalls, paymentCalls int
}

func (f *fakeTrialAPI) OrgDetails(ctx context.Context, org string) (*cloudapi.OrgDetails, error) {
	f.detailsCalls++
	return &cloudapi.OrgDetails{Name: org, Subscription: cloudapi.SubscriptionInfo{Status: f.status, TrialEnd: f.trialEnd}}, nil
}

func (f *fakeTrialAPI) OrgHasPaymentMethod(ctx context.Context, org string) (bool, error) {
	f.paymentCalls++
	return f.hasPaymentMethod, f.paymentErr
}

func TestCheckTrialStatus(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	end := "2026-09-30T12:00:00Z"

	t.Run("trial without a payment method", func(t *testing.T) {
		api := &fakeTrialAPI{status: "in_trial", trialEnd: &end}
		status, err := checkTrialStatus(ctx, api, "acme", now)
		require.NoError(t, err)
		require.True(t, status.NeedsPayment)
		require.NotNil(t, status.TrialEnd)
		require.Equal(t, end, status.TrialEnd.Format(time.RFC3339))
	})

	t.Run("trial with a payment method", func(t *testing.T) {
		status, err := checkTrialStatus(ctx, &fakeTrialAPI{status: "in_trial", hasPaymentMethod: true}, "acme", now)
		require.NoError(t, err)
		require.False(t, status.NeedsPayment)
	})

	t.Run("paid orgs do not ask for the payment method", func(t *testing.T) {
		api := &fakeTrialAPI{status: "active"}
		status, err := checkTrialStatus(ctx, api, "acme", now)
		require.NoError(t, err)
		require.False(t, status.NeedsPayment)
		require.Zero(t, api.paymentCalls, "the billing lookup is only for trials")
	})

	t.Run("a failed payment method lookup is an error, not a reminder", func(t *testing.T) {
		_, err := checkTrialStatus(ctx, &fakeTrialAPI{status: "in_trial", paymentErr: errors.New("no such field")}, "acme", now)
		require.Error(t, err)
	})
}

func TestTrialStatusCache(t *testing.T) {
	oldCache := xdg.CacheHome
	xdg.CacheHome = t.TempDir()
	t.Cleanup(func() { xdg.CacheHome = oldCache })

	ctx := context.Background()
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	api := &fakeTrialAPI{status: "in_trial"}

	status, err := trialStatusFor(ctx, api, "acme", now)
	require.NoError(t, err)
	require.True(t, status.NeedsPayment)
	require.Equal(t, 1, api.paymentCalls)

	// A recent result is reused.
	status, err = trialStatusFor(ctx, api, "acme", now.Add(trialReminderCacheTTL-time.Minute))
	require.NoError(t, err)
	require.True(t, status.NeedsPayment)
	require.Equal(t, 1, api.paymentCalls)

	// Other orgs are checked on their own.
	_, err = trialStatusFor(ctx, api, "other", now)
	require.NoError(t, err)
	require.Equal(t, 2, api.paymentCalls)

	// An old result is checked again.
	api.hasPaymentMethod = true
	status, err = trialStatusFor(ctx, api, "acme", now.Add(trialReminderCacheTTL+time.Minute))
	require.NoError(t, err)
	require.False(t, status.NeedsPayment)
	require.Equal(t, 3, api.paymentCalls)

	// Opening the payment page drops the result.
	forgetTrialStatus("acme")
	api.hasPaymentMethod = false
	status, err = trialStatusFor(ctx, api, "acme", now.Add(trialReminderCacheTTL+2*time.Minute))
	require.NoError(t, err)
	require.True(t, status.NeedsPayment)
	require.Equal(t, 4, api.paymentCalls)

	// Failures are not cached.
	forgetTrialStatus("acme")
	api.paymentErr = errors.New("unavailable")
	_, err = trialStatusFor(ctx, api, "acme", now)
	require.Error(t, err)
	require.Nil(t, cachedTrialStatus("acme", now))
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
	require.Contains(t, out, `Enter payment details with "dagger cloud billing payment" to avoid service disruption once your trial ends.`)
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
