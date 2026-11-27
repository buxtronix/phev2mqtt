//go:build !linux

package cmd

// watchLink needs Linux netlink; elsewhere there is no WiFi status.
func watchLink(iface string, fn func(up bool)) error {
	return errNoNetlink
}
