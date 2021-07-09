/*
Copyright © 2021 Ben Buxton <bbuxton@gmail.com>

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program. If not, see <http://www.gnu.org/licenses/>.
*/
package cmd

import (
	"encoding/hex"
	"strings"

	"github.com/buxtronix/phev2mqtt/client"
	//	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// setCmd represents the set command
var setCmd = &cobra.Command{
	Use:   "set",
	Short: "Set register value(s)",
	Args:  cobra.MinimumNArgs(1),
	Long: `Send new values to the car to set register(s).

Each arg is of the form <register>:<value> - the given register is set to the
given value. Each should be a hex string.

e.g:

0b:02 will set register 0b to the value 02

`,
	Run: runSet,
}

type regValue struct {
	register byte
	value    []byte
}

func runSet(cmd *cobra.Command, args []string) {
	var register, value []byte
	var err error

	setRegisters := []*regValue{}

	for _, arg := range args {
		if vals := strings.Split(arg, ":"); len(vals) == 2 {
			register, err = hex.DecodeString(vals[0])
			if err != nil {
				panic(err)
			}
			value, err = hex.DecodeString(vals[1])
			if err != nil {
				panic(err)
			}
			setRegisters = append(setRegisters, &regValue{
				register: register[0], value: value,
			})
		}
	}

	address, _ := cmd.Flags().GetString("address")
	cl, err := client.New(client.AddressOption(address))
	if err != nil {
		panic(err)
	}

	if err := cl.Connect(); err != nil {
		panic(err)
	}

	if err := cl.Start(); err != nil {
		panic(err)
	}

	for _, reg := range setRegisters {
		if err := cl.SetRegister(reg.register, reg.value); err != nil {
			panic(err)
		}
	}

}

func init() {
	clientCmd.AddCommand(setCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// registerCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// registerCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
