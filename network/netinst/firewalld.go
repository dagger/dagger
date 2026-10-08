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
// isFirewalldRunning). Any failure, including no system bus, counts as not
// running.
func FirewalldRunning(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, firewalldProbeTimeout)
	defer cancel()
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return false
	}
	defer conn.Close()
	var owner string
	return conn.Object("org.freedesktop.DBus", "/org/freedesktop/DBus").
		CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, "org.fedoraproject.FirewallD1").
		Store(&owner) == nil
}
