package daggercmd

import (
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/muesli/termenv"
	"github.com/pkg/browser"
	"github.com/spf13/cobra"

	"github.com/dagger/dagger/dagql/idtui"
	cloudapi "github.com/dagger/dagger/internal/cloud"
)

var billingOpen bool

var cloudBillingCmd = newBillingCmd(false)

// billingCmd is a hidden alias for 'dagger cloud billing'.
var billingCmd = newBillingCmd(true)

func init() {
	cloudCmd.AddCommand(cloudBillingCmd)
	rootCmd.AddCommand(billingCmd)
}

func newBillingCmd(hidden bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "billing [org]",
		Short: "Manage your Dagger Cloud subscription, payment method, and billing information",
		Long: `Manage your Dagger Cloud subscription, payment method, and billing information.

Opens the billing portal of the org (the argument, or the current org) in a
browser when running in an interactive terminal, and prints its URL.`,
		Args:   cobra.MaximumNArgs(1),
		Hidden: hidden,
		RunE:   cloudCLI.Billing,
	}
	cmd.Flags().BoolVar(&cloudJSON, "json", false, "Print JSON output")
	cmd.Flags().BoolVar(&billingOpen, "open", false, "Open the billing portal in a browser (default: in an interactive terminal)")
	return cmd
}

// Billing resolves the target org (the argument, or the current org), asks
// Cloud for its billing portal, and reports it: opened in a browser
// when that can reach the user, as JSON with --json, otherwise printed.
func (cli *CloudCLI) Billing(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	client, cloudAuth, err := cli.cloudClient(ctx)
	if err != nil {
		return err
	}
	var org *cloudapi.OrgResponse
	if len(args) > 0 {
		org, err = client.OrgByName(ctx, args[0])
	} else {
		org, err = cli.resolveCloudOrg(ctx, client, cloudAuth)
	}
	if err != nil {
		return err
	}
	url, err := client.CreatePortalSession(ctx, org.ID)
	if err != nil {
		return err
	}

	// Open the page like dagger login does: automatically in an interactive
	// terminal, unless --open says otherwise.
	open := billingOpen
	if !cmd.Flags().Changed("open") {
		open = canOpenBrowser()
	}
	browserOpened := open && openBrowser(url)

	if cloudJSON {
		return writeCloudJSON(cmd, map[string]any{"org": org, "url": url, "opened": browserOpened})
	}
	printBillingPortal(cmd.OutOrStdout(), org.Name, url, browserOpened, stdoutIsTTY)
	return nil
}

// printBillingPortal reports the billing portal URL. Piped output is the URL
// alone, so scripts keep working.
func printBillingPortal(w io.Writer, org, url string, opened, interactive bool) {
	if !interactive {
		fmt.Fprintln(w, url)
		return
	}
	out := idtui.NewOutput(w)
	if opened {
		fmt.Fprintf(w, "%s Opened the billing portal for %s in your browser.\n",
			out.String("✓").Foreground(termenv.ANSIGreen), out.String(org).Bold())
		fmt.Fprintln(w, "Use it to manage your subscription, payment method, and billing information. If it didn't open, visit:")
	} else {
		fmt.Fprintf(w, "Open the billing portal to manage the subscription, payment method, and billing information of %s:\n", out.String(org).Bold())
	}
	fmt.Fprintf(w, "  %s\n", out.String(url).Underline())
}

// canOpenBrowser reports whether opening a browser can reach the user: an
// interactive terminal on the machine with a graphical session.
func canOpenBrowser() bool {
	if !canOpenShellOnError(progress, stdinIsTTY) || cloudJSON {
		return false
	}
	// Over SSH the browser would open on the remote machine.
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return false
	}
	// Without a display, the fallback can be a text browser that takes over
	// the terminal.
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
		return false
	}
	return true
}

// openBrowser opens url and reports whether it did. The launcher's own output
// is dropped, as dagger login does: the command prints the URL either way.
func openBrowser(url string) bool {
	stdout, stderr := browser.Stdout, browser.Stderr
	defer func() { browser.Stdout, browser.Stderr = stdout, stderr }()
	browser.Stdout, browser.Stderr = io.Discard, io.Discard
	return browser.OpenURL(url) == nil
}
