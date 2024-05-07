package netinst

import (
	"context"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
)

func InstallDnsmasq(ctx context.Context, name string) error {
	dnsmasqPath, err := exec.LookPath("dnsmasq")
	if err != nil {
		return err
	}

	hostsFile := hostsPath(name)
	if err := createIfNeeded(hostsFile); err != nil {
		return err
	}

	config := dnsmasqConfig{
		Domain:             name + ".local",
		NetworkInterface:   name + "0",
		PidFile:            pidfilePath(name),
		AddnHostsFile:      hostsFile,
		UpstreamResolvFile: upstreamResolvPath,
	}

	dnsmasqConfigFile := dnsmasqConfPath(name)

	if err := writeDnsmasqConfig(dnsmasqConfigFile, config); err != nil {
		return fmt.Errorf("write dnsmasq.conf: %w", err)
	}

	dnsmasq := exec.CommandContext(ctx,
		dnsmasqPath,
		"--keep-in-foreground",
		"--log-facility=-",
		"--log-debug",
		"-u", "root",
		"--conf-file="+dnsmasqConfigFile,
	)

	// forward dnsmasq logs to engine logs for debugging
	dnsmasq.Stdout = os.Stdout
	dnsmasq.Stderr = os.Stderr

	if err := dnsmasq.Start(); err != nil {
		return fmt.Errorf("start dnsmasq: %w", err)
	}

	go func() {
		err := dnsmasq.Wait()
		if err != nil {
			fmt.Fprintf(os.Stderr, "dnsmasq exited: %v\n", err)
		}
	}()

	return nil
}

func writeDnsmasqConfig(dnsmasqConfigFile string, config dnsmasqConfig) error {
	if err := os.MkdirAll(filepath.Dir(dnsmasqConfigFile), 0700); err != nil {
		return err
	}

	conf, err := os.OpenFile(dnsmasqConfigFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}

	defer conf.Close()

	tmpl, err := template.New("dnsmasq.conf").Parse(dnsmasqTemplate)
	if err != nil {
		return err
	}

	if err := tmpl.Execute(conf, config); err != nil {
		return err
	}

	return conf.Close()
}

const dnsmasqTemplate = `## WARNING: THIS IS AN AUTOGENERATED FILE
## AND SHOULD NOT BE EDITED MANUALLY AS IT
## LIKELY TO AUTOMATICALLY BE REPLACED.
strict-order
local=/{{.Domain}}/
domain={{.Domain}}
domain-needed
expand-hosts
pid-file={{.PidFile}}
except-interface=lo
interface={{.NetworkInterface}}
addn-hosts={{.AddnHostsFile}}
resolv-file={{.UpstreamResolvFile}}
`

type dnsmasqConfig struct {
	Domain             string
	NetworkInterface   string
	PidFile            string
	AddnHostsFile      string
	UpstreamResolvFile string
}
