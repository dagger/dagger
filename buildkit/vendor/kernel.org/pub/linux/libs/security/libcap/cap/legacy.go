// +build linux,arm linux,386

package cap

import "syscall"

var sysSetGroupsVariant = uintptr(syscall.SYS_SETGROUPS32)
