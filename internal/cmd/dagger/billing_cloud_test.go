package daggercmd

import (
	"bytes"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrintBillingPage(t *testing.T) {
	page := billingPage{name: "payment page", action: "add or update the payment method"}
	const url = "https://dagger-test.chargebee.com/pages/v4/abc/"

	t.Run("opened in the browser", func(t *testing.T) {
		var buf bytes.Buffer
		printBillingPage(&buf, page, "acme", url, true, true)
		require.Equal(t,
			"✓ Opened the payment page for acme in your browser.\n"+
				"Use it to add or update the payment method. If it didn't open, visit:\n"+
				"  "+url+"\n",
			buf.String())
	})

	t.Run("not opened", func(t *testing.T) {
		var buf bytes.Buffer
		printBillingPage(&buf, page, "acme", url, false, true)
		require.Equal(t,
			"Open the payment page to add or update the payment method for acme:\n"+
				"  "+url+"\n",
			buf.String())
	})

	t.Run("piped output is the URL alone", func(t *testing.T) {
		var buf bytes.Buffer
		printBillingPage(&buf, page, "acme", url, true, false)
		require.Equal(t, url+"\n", buf.String())
	})
}

func TestCanOpenBrowser(t *testing.T) {
	prevProgress, prevTTY, prevJSON := progress, stdinIsTTY, cloudJSON
	t.Cleanup(func() { progress, stdinIsTTY, cloudJSON = prevProgress, prevTTY, prevJSON })

	interactive := func(t *testing.T) {
		progress, stdinIsTTY, cloudJSON = "tty", true, false
		t.Setenv("SSH_CONNECTION", "")
		t.Setenv("SSH_TTY", "")
		t.Setenv("DISPLAY", ":0")
		t.Setenv("WAYLAND_DISPLAY", "")
	}

	interactive(t)
	require.True(t, canOpenBrowser())

	for name, change := range map[string]func(){
		"plain progress": func() { progress = "plain" },
		"no terminal":    func() { stdinIsTTY = false },
		"--json":         func() { cloudJSON = true },
		"ssh session":    func() { t.Setenv("SSH_CONNECTION", "10.0.0.1 22 10.0.0.2 22") },
		"ssh terminal":   func() { t.Setenv("SSH_TTY", "/dev/pts/1") },
	} {
		t.Run(name, func(t *testing.T) {
			interactive(t)
			change()
			require.False(t, canOpenBrowser())
		})
	}

	if runtime.GOOS == "linux" {
		t.Run("linux without a display", func(t *testing.T) {
			interactive(t)
			t.Setenv("DISPLAY", "")
			require.False(t, canOpenBrowser())
			t.Setenv("WAYLAND_DISPLAY", "wayland-0")
			require.True(t, canOpenBrowser())
		})
	}
}
