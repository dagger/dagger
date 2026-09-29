package daggercmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/adrg/xdg"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/dagql/idtui"
	cloudapi "github.com/dagger/dagger/internal/cloud"
	cloudauth "github.com/dagger/dagger/internal/cloud/auth"
)

// A trial reminder tells users whose Cloud org is in its trial without a
// payment method to enter one, after the command's trace output.

// trialReminderCacheTTL is how long a check result is reused. Whether an org
// has a payment method is looked up in the billing system on every request,
// and the CLI runs often, so the answer is not asked for on every command.
const trialReminderCacheTTL = 15 * time.Minute

// trialStatus is the outcome of a trial reminder check for one org.
type trialStatus struct {
	// NeedsPayment is set when the org is in its trial without a payment
	// method.
	NeedsPayment bool       `json:"needsPayment"`
	TrialEnd     *time.Time `json:"trialEnd,omitempty"`
	CheckedAt    time.Time  `json:"checkedAt"`
}

// trialReminderAPI is the part of the Cloud client the check uses.
type trialReminderAPI interface {
	OrgDetails(ctx context.Context, orgName string) (*cloudapi.OrgDetails, error)
	OrgHasPaymentMethod(ctx context.Context, orgName string) (bool, error)
}

// checkTrialStatus asks Cloud whether org is in its trial without a payment
// method. The payment method is only asked for orgs in their trial.
func checkTrialStatus(ctx context.Context, api trialReminderAPI, org string, now time.Time) (*trialStatus, error) {
	details, err := api.OrgDetails(ctx, org)
	if err != nil {
		return nil, err
	}
	status := &trialStatus{CheckedAt: now}
	if details.Subscription.Status != "in_trial" {
		return status, nil
	}
	hasPaymentMethod, err := api.OrgHasPaymentMethod(ctx, org)
	if err != nil {
		return nil, err
	}
	status.NeedsPayment = !hasPaymentMethod
	if end := details.Subscription.TrialEnd; end != nil {
		if t, err := time.Parse(time.RFC3339, *end); err == nil {
			status.TrialEnd = &t
		}
	}
	return status, nil
}

func trialReminderCachePath(org string) string {
	name := strings.NewReplacer("/", "_", "\\", "_").Replace(org)
	return filepath.Join(xdg.CacheHome, "dagger", "trial-reminder", name+".json")
}

// cachedTrialStatus returns the cached check result for org, if it is recent.
func cachedTrialStatus(org string, now time.Time) *trialStatus {
	data, err := os.ReadFile(trialReminderCachePath(org))
	if err != nil {
		return nil
	}
	var status trialStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return nil
	}
	if now.Sub(status.CheckedAt) > trialReminderCacheTTL || status.CheckedAt.After(now) {
		return nil
	}
	return &status
}

func cacheTrialStatus(org string, status *trialStatus) {
	path := trialReminderCachePath(org)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	data, err := json.Marshal(status)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o600)
}

// forgetTrialStatus drops the cached check result for org, so the next
// command checks again, for example after entering payment details.
func forgetTrialStatus(org string) {
	_ = os.Remove(trialReminderCachePath(org))
}

// trialStatusFor returns the check result for org, from the cache when it is
// recent and from Cloud otherwise.
func trialStatusFor(ctx context.Context, api trialReminderAPI, org string, now time.Time) (*trialStatus, error) {
	if status := cachedTrialStatus(org, now); status != nil {
		return status, nil
	}
	status, err := checkTrialStatus(ctx, api, org, now)
	if err != nil {
		return nil, err
	}
	cacheTrialStatus(org, status)
	return status, nil
}

// skipNag reports whether the user asked to hide CLI nags.
func skipNag() bool {
	for _, env := range idtui.SkipLoggedOutTraceMsgEnvs {
		if os.Getenv(env) != "" {
			return true
		}
	}
	return false
}

var startTrialReminderOnce sync.Once

// startTrialReminder checks in the background whether the current org is in
// its trial without a payment method, and reminds the user after the command
// if so. Like the update check, it never delays the command: without an
// answer by then, nothing is printed.
func startTrialReminder(ctx context.Context, w io.Writer) {
	if skipNag() {
		return
	}
	startTrialReminderOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		type orgStatus struct {
			org    string
			status *trialStatus
		}
		result := make(chan orgStatus, 1)
		go func() {
			defer close(result)
			org, err := cloudauth.CurrentOrgName()
			if err != nil || org == "" {
				return
			}
			cloudAuth, err := cloudauth.GetCloudAuth(ctx)
			if err != nil || cloudAuth == nil || cloudAuth.Token == nil {
				return
			}
			client, err := cloudapi.NewClient(ctx, cloudAuth)
			if err != nil {
				return
			}
			// Errors are not reported: an older Cloud API without the payment
			// method field, or a failed lookup, must not add noise.
			status, err := trialStatusFor(ctx, client, org, time.Now())
			if err != nil {
				return
			}
			result <- orgStatus{org: org, status: status}
		}()

		cobra.OnFinalize(func() {
			defer cancel()
			select {
			case r, ok := <-result:
				if ok && r.status.NeedsPayment {
					trialReminder(w, r.org, r.status.TrialEnd, time.Now())
				}
			default:
			}
		})
	})
}

// trialReminder prints the reminder to enter payment details.
func trialReminder(w io.Writer, org string, trialEnd *time.Time, now time.Time) {
	output := idtui.NewOutput(w)
	fmt.Fprint(w, "\r\n"+
		output.String(trialReminderHeadline(org, trialEnd, now)).Foreground(termenv.ANSIYellow).String()+"\n"+
		`Enter payment details with "dagger cloud billing payment" to avoid service disruption once your trial ends.`+"\n"+
		output.String(fmt.Sprintf("To hide set %s=1", idtui.SkipLoggedOutTraceMsgEnvs[0])).Faint().String()+"\n",
	)
}

func trialReminderHeadline(org string, trialEnd *time.Time, now time.Time) string {
	headline := fmt.Sprintf("You are on a free Dagger Cloud trial for %s", org)
	if trialEnd == nil {
		return headline + "."
	}
	switch days := int(math.Ceil(trialEnd.Sub(now).Hours() / 24)); {
	case days <= 0:
		return headline + ", ending today."
	case days == 1:
		return headline + ", ending in 1 day."
	default:
		return fmt.Sprintf("%s, ending in %d days.", headline, days)
	}
}
