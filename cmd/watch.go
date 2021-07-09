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
	"time"

	"github.com/buxtronix/phev2mqtt/client"
	"github.com/buxtronix/phev2mqtt/protocol"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// watchCmd represents the watch command
var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Connect to Phev and watch incoming updates",
	Long: `Connects to the Phev and watches for incoming register
updates, displaying them in real-time.`,
	Run: Run,
}

func Run(cmd *cobra.Command, args []string) {
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

	var registers = map[byte]string{}

	for {
		select {
		case m, ok := <-cl.Recv:
			if !ok {
				log.Infof("Connection closed.")
				return
			}
			switch m.Type {
			case protocol.CmdInResp:
				dataStr := hex.EncodeToString(m.Data)
				if data := registers[m.Register]; data != dataStr {
					log.Infof("%%PHEV_REG_UPDATE%% %02x: %s -> %s", m.Register, data, dataStr)
					registers[m.Register] = dataStr
					if _, ok := m.Reg.(*protocol.RegisterGeneric); !ok {
						log.Infof("%%PHEV_REG_UPDATE%% %02x: [%s]", m.Register, m.Reg.String())
					}
				}
				cl.Send <- &protocol.PhevMessage{
					Type:     protocol.CmdOutSend,
					Register: m.Register,
					Ack:      protocol.Ack,
					Xor:      m.Xor,
					Data:     []byte{0x0},
				}
			}
		}
	}
}

func init() {
	clientCmd.AddCommand(watchCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// watchCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// watchCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
	watchCmd.Flags().DurationP("wait", "w", 60*time.Second, "How long to hold connection open for")
}
