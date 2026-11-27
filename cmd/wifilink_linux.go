package cmd

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

const (
	// IF_OPER_UP from <linux/if.h>. A WiFi interface only gets there once it
	// is associated and, with WPA, the handshake is done.
	ifOperUp = 6
	// RTMGRP_LINK from <linux/rtnetlink.h>: link changes; syscall lacks it.
	rtmgrpLink = 0x1
)

// watchLink calls fn with whether iface is up, once at the start and then on
// every change. The kernel announces the changes over netlink, so nothing is
// polled. It only returns on an error.
func watchLink(iface string, fn func(up bool)) error {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("netlink socket: %w", err)
	}
	defer syscall.Close(fd)
	if err := syscall.Bind(fd, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK, Groups: rtmgrpLink}); err != nil {
		return fmt.Errorf("netlink bind: %w", err)
	}

	// Read the state only after subscribing, so no change falls in between.
	state, _ := os.ReadFile("/sys/class/net/" + iface + "/operstate")
	up := strings.TrimSpace(string(state)) == "up"
	fn(up)

	buf := make([]byte, 1<<16)
	for {
		n, _, err := syscall.Recvfrom(fd, buf, 0)
		if err != nil {
			if err == syscall.EINTR || err == syscall.ENOBUFS {
				continue
			}
			return fmt.Errorf("netlink receive: %w", err)
		}
		msgs, err := syscall.ParseNetlinkMessage(buf[:n])
		if err != nil {
			continue
		}
		for _, msg := range msgs {
			now, ok := linkUp(&msg, iface)
			if ok && now != up {
				up = now
				fn(up)
			}
		}
	}
}

// linkUp reports whether msg says iface is up; ok is false when msg is not
// about iface. The kernel also sends RTM_NEWLINK for changes that are not
// about the state, so the caller drops repeats.
func linkUp(msg *syscall.NetlinkMessage, iface string) (up, ok bool) {
	if msg.Header.Type != syscall.RTM_NEWLINK && msg.Header.Type != syscall.RTM_DELLINK {
		return false, false
	}
	attrs, err := syscall.ParseNetlinkRouteAttr(msg)
	if err != nil {
		return false, false
	}
	name, operState := "", -1
	for _, a := range attrs {
		switch a.Attr.Type {
		case syscall.IFLA_IFNAME:
			name = strings.TrimRight(string(a.Value), "\x00")
		case syscall.IFLA_OPERSTATE:
			if len(a.Value) > 0 {
				operState = int(a.Value[0])
			}
		}
	}
	if name != iface {
		return false, false
	}
	if msg.Header.Type == syscall.RTM_DELLINK {
		return false, true
	}
	return operState == ifOperUp, operState >= 0
}
