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
	"github.com/buxtronix/phev2mqtt/emulator"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// emulatorCmd represents the emulate command
var emulatorCmd = &cobra.Command{
	Use:   "emulator",
	Short: "Emulate a car, to enable app testing",
	Long:  `Starts up a service that emulates a car.`,
	Run: func(cmd *cobra.Command, args []string) {
		address, _ := cmd.Flags().GetString("address")
		car, err := emulator.NewCar(emulator.AddressOption(address))
		if err != nil {
			panic(err)
		}
		if err := car.Begin(); err != nil {
			log.Errorf(err.Error())
			return
		}
		select {}
	},
}

func init() {
	rootCmd.AddCommand(emulatorCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// watchCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	emulatorCmd.PersistentFlags().String("address", ":8080", "Address to listen on")
}
