package cmd

import (
	"encoding/binary"
	"syscall"
	"testing"
)

// linkMessage builds the netlink message the kernel sends for a link change.
func linkMessage(typ uint16, name string, operState byte) syscall.NetlinkMessage {
	attr := func(t uint16, v []byte) []byte {
		b := make([]byte, syscall.SizeofRtAttr, syscall.SizeofRtAttr+len(v)+3)
		binary.NativeEndian.PutUint16(b[0:], uint16(syscall.SizeofRtAttr+len(v)))
		binary.NativeEndian.PutUint16(b[2:], t)
		b = append(b, v...)
		for len(b)%4 != 0 {
			b = append(b, 0)
		}
		return b
	}
	data := make([]byte, syscall.SizeofIfInfomsg)
	data = append(data, attr(syscall.IFLA_IFNAME, append([]byte(name), 0))...)
	data = append(data, attr(syscall.IFLA_OPERSTATE, []byte{operState})...)
	return syscall.NetlinkMessage{Header: syscall.NlMsghdr{Type: typ}, Data: data}
}

func TestLinkUp(t *testing.T) {
	const ifOperDormant = 5
	for _, tc := range []struct {
		name   string
		msg    syscall.NetlinkMessage
		up, ok bool
	}{
		{"up", linkMessage(syscall.RTM_NEWLINK, "wlan0", ifOperUp), true, true},
		{"handshake not done", linkMessage(syscall.RTM_NEWLINK, "wlan0", ifOperDormant), false, true},
		{"removed", linkMessage(syscall.RTM_DELLINK, "wlan0", ifOperUp), false, true},
		{"other interface", linkMessage(syscall.RTM_NEWLINK, "eth0", ifOperUp), false, false},
		{"not a link message", linkMessage(syscall.RTM_NEWADDR, "wlan0", ifOperUp), false, false},
	} {
		up, ok := linkUp(&tc.msg, "wlan0")
		if up != tc.up || ok != tc.ok {
			t.Errorf("%s: linkUp = %v, %v; want %v, %v", tc.name, up, ok, tc.up, tc.ok)
		}
	}
}
