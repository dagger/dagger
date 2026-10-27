package daggercmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/dagql/idtui"
	cloudapi "github.com/dagger/dagger/internal/cloud"
	cloudauth "github.com/dagger/dagger/internal/cloud/auth"
)

// A Cloud notice is a message Cloud has for the user about the org their
// trace goes to, such as a trial reminder, shown after the command's trace
// output. Cloud chooses the notice and its text; the CLI only shows it.

// skipNag reports whether the user asked to hide CLI nags.
func skipNag() bool {
	for _, env := range idtui.SkipLoggedOutTraceMsgEnvs {
		if os.Getenv(env) != "" {
			return true
		}
	}
	return false
}

var startCloudNoticeOnce sync.Once

// startCloudNotice asks Cloud in the background for a notice about the
// current org, and shows it after the command. Like the update check, it
// never delays the command: without an answer by then, nothing is printed.
func startCloudNotice(ctx context.Context, w io.Writer) {
	if skipNag() {
		return
	}
	startCloudNoticeOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		result := make(chan *cloudapi.CLINotice, 1)
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
			// Errors are not reported: an older Cloud API without notices, or
			// a failed lookup, must not add noise.
			notice, err := client.OrgCLINotice(ctx, org)
			if err != nil {
				return
			}
			result <- notice
		}()

		cobra.OnFinalize(func() {
			defer cancel()
			select {
			case notice, ok := <-result:
				if ok {
					printCloudNotice(w, notice)
				}
			default:
			}
		})
	})
}

// printCloudNotice shows notice, if any, with a hint on hiding it.
func printCloudNotice(w io.Writer, notice *cloudapi.CLINotice) {
	if notice == nil || notice.Title == "" {
		return
	}
	output := idtui.NewOutput(w)
	title := output.String(notice.Title)
	if notice.Level == cloudapi.CLINoticeLevelWarning {
		title = title.Foreground(termenv.ANSIYellow)
	} else {
		title = title.Bold()
	}
	var b strings.Builder
	b.WriteString("\r\n")
	b.WriteString(title.String() + "\n")
	if body := strings.TrimRight(notice.Body, "\n"); body != "" {
		b.WriteString(body + "\n")
	}
	b.WriteString(output.String(fmt.Sprintf("To hide set %s=1", idtui.SkipLoggedOutTraceMsgEnvs[0])).Faint().String() + "\n")
	fmt.Fprint(w, b.String())
}
