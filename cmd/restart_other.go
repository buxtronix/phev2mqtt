//go:build !unix

package cmd

import "errors"

func execBinary() error {
	return errors.New("restarting needs a Unix system")
}
