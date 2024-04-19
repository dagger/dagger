package helpers

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/moby/buildkit/util/testutil/integration"
	"github.com/pkg/errors"
)

const (
	azuriteBin = "azurite-blob"
)

type AzuriteOpts struct {
	AccountName string
	AccountKey  string
}

func NewAzuriteServer(t *testing.T, sb integration.Sandbox, opts AzuriteOpts) (address string, cl func() error, err error) {
	t.Helper()

	if _, err := exec.LookPath(azuriteBin); err != nil {
		return "", nil, errors.Wrapf(err, "failed to lookup %s binary", azuriteBin)
	}

	deferF := &integration.MultiCloser{}
	cl = deferF.F()

	defer func() {
		if err != nil {
			deferF.F()()
			cl = nil
		}
	}()

	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return "", nil, err
	}

	addr := l.Addr().String()
	if err = l.Close(); err != nil {
		return "", nil, err
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", nil, err
	}
	address = fmt.Sprintf("http://%s/%s", addr, opts.AccountName)

	// start server
	cmd := exec.Command(azuriteBin, "--disableProductStyleUrl", "--blobHost", host, "--blobPort", port, "--location", t.TempDir())
	cmd.Env = append(os.Environ(), []string{
		"AZURITE_ACCOUNTS=" + opts.AccountName + ":" + opts.AccountKey,
	}...)
	azuriteStop, err := integration.StartCmd(cmd, sb.Logs())
	if err != nil {
		return "", nil, err
	}
	if err = waitAzurite(address, 15*time.Second); err != nil {
		azuriteStop()
		return "", nil, errors.Wrapf(err, "azurite did not start up: %s", integration.FormatLogs(sb.Logs()))
	}
	deferF.Append(azuriteStop)

	return
}

func waitAzurite(address string, d time.Duration) error {
	step := 1 * time.Second
	i := 0
	for {
		if resp, err := http.Get(fmt.Sprintf("%s?comp=list", address)); err == nil {
			resp.Body.Close()
			break
		}
		i++
		if time.Duration(i)*step > d {
			return errors.Errorf("failed dialing: %s", address)
		}
		time.Sleep(step)
	}
	return nil
}
