package daggercmd

import (
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"sync"
	"time"

	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/dagql/idtui"
	cloudapi "github.com/dagger/dagger/internal/cloud"
	cloudauth "github.com/dagger/dagger/internal/cloud/auth"
)

// A trial reminder tells users whose Cloud org is in its trial without a
// payment method to enter one, after the command's trace output.

// trialStatus is the outcome of a trial reminder check for one org.
type trialStatus struct {
	// NeedsPayment is set when the org is in its trial without a payment
	// method.
	NeedsPayment bool
	TrialEnd     *time.Time
}

// trialReminderAPI is the part of the Cloud client the check uses.
type trialReminderAPI interface {
	OrgPaymentStatus(ctx context.Context, orgName string) (*cloudapi.PaymentStatus, error)
}

// checkTrialStatus asks Cloud whether org is in its trial without a payment
// method. Cloud keeps the payment method current from billing webhooks, so
// this is one cheap query.
func checkTrialStatus(ctx context.Context, api trialReminderAPI, org string) (*trialStatus, error) {
	payment, err := api.OrgPaymentStatus(ctx, org)
	if err != nil {
		return nil, err
	}
	status := &trialStatus{}
	if payment.Status != "in_trial" {
		return status, nil
	}
	status.NeedsPayment = !payment.HasPaymentMethod
	if end := payment.TrialEnd; end != nil {
		if t, err := time.Parse(time.RFC3339, *end); err == nil {
			status.TrialEnd = &t
		}
	}
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
			status, err := checkTrialStatus(ctx, client, org)
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
