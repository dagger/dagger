package netinst

import (
	"context"
	"time"

	"github.com/godbus/dbus/v5"
)

// firewalldProbeTimeout bounds FirewalldRunning, which runs at engine start.
const firewalldProbeTimeout = 2 * time.Second

// FirewalldRunning reports whether firewalld answers on the system D-Bus. It
// is the check the CNI firewall plugin makes to manage container traffic
// through firewalld rather than iptables (plugins/meta/firewall,
// isFirewalldRunning). Any failure, including no system bus or no answer
// within the timeout, counts as not running.
func FirewalldRunning(ctx context.Context) bool {
	return firewalldRunning(ctx, firewalldProbeTimeout)
}

func firewalldRunning(ctx context.Context, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// godbus connects before it applies the context, so probe in the
	// background and stop waiting at the deadline.
	result := make(chan bool, 1)
	go func() {
		conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
		if err != nil {
			result <- false
			return
		}
		defer conn.Close()
		var owner string
		result <- conn.Object("org.freedesktop.DBus", "/org/freedesktop/DBus").
			CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, "org.fedoraproject.FirewallD1").
			Store(&owner) == nil
	}()
	select {
	case running := <-result:
		return running
	case <-ctx.Done():
		return false
	}
}
