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
	"fmt"
	"time"

	"github.com/buxtronix/phev2mqtt/client"
	"github.com/buxtronix/phev2mqtt/protocol"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// registerCmd represents the register command
var registerCmd = &cobra.Command{
	Use:   "register",
	Short: "Register client with the car",
	Long: `Register phev2mqtt with the car.

You will need to first put the car into registration mode, then run
this command within 5 minutes.

The car will register the MAC address that connects to it - typically the
MAC address of the TCP client that ultimately connects to the car. If you
are going via a NAT gateway, etc, then bear this in mind.

`,
	Run: runRegister,
}

// unRegisterCmd represents the unregister command
var unRegisterCmd = &cobra.Command{
	Use:   "unregister",
	Short: "Un register client from the car",
	Long: `Unregister this phev2mqtt instance from the car.

The car will unregister the MAC address that connects to it - typically the
MAC address of the TCP client that ultimately connects to the car. If you
are going via a NAT gateway, etc, then bear this in mind.

`,
	Run: runRegister,
}

func runRegister(cmd *cobra.Command, args []string) {
	var err error

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
	fmt.Printf("Client connected and started!\n")

	vinCh := make(chan string)

	go func() {
		for {
			select {
			case msg, ok := <-cl.Recv:
				if !ok {
					log.Errorf("Connection closed.")
					close(vinCh)
					return
				}
				switch msg.Type {
				case protocol.CmdInResp:
					if msg.Ack != protocol.Request {
						break
					}
					if reg, ok := msg.Reg.(*protocol.RegisterVIN); ok {
						vinCh <- reg.VIN
					}
					cl.Send <- &protocol.PhevMessage{
						Type:     protocol.CmdOutSend,
						Register: msg.Register,
						Ack:      protocol.Ack,
						Xor:      msg.Xor,
						Data:     []byte{0x0},
					}
				}
			}
		}
	}()

	vin, ok := <-vinCh
	if !ok {
		log.Errorf("Client closed before recieving VIN")
		return
	}

	reg := byte(0x10)
	if cmd.Use == "unregister" {
		fmt.Printf("Attempting to unregister from car (VIN: %s)...\n", vin)
		reg = 0x15
	} else {
		fmt.Printf("Attempting to register to car (VIN: %s)...\n", vin)
	}
	if err := cl.SetRegister(reg, []byte{0x1}); err != nil {
		log.Errorf("Failed to (un)register: %v", err)
		return
	}
	cl.Close()
	time.Sleep(time.Second)
	fmt.Printf("Success!\n")
}

func init() {
	clientCmd.AddCommand(registerCmd)
	clientCmd.AddCommand(unRegisterCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// registerCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// registerCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
	registerCmd.Flags().Duration("wait_duration", 10*time.Second, "How long to wait after connecting to car before sending registration command")
	unRegisterCmd.Flags().Duration("wait_duration", 10*time.Second, "How long to wait after connecting to car before sending registration command")
}
