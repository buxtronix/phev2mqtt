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
	"os"
	"strings"

	"github.com/buxtronix/phev2mqtt/protocol"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

// fileCmd represents the file command
var fileCmd = &cobra.Command{
	Use:   "file",
	Short: "Decode Phev messages from a file",
	Long: `
Decode raw hex string messages from the provided filename.
`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		f, err := os.Open(args[0])
		if err != nil {
			panic(err)
		}
		defer f.Close()

		inData := make([]byte, 100000)
		n, err := f.Read(inData)
		if err != nil {
			panic(err)
		}
		dat := strings.Replace(string(inData[:n]), "\n", "", -1)
		binData, err := hex.DecodeString(dat)
		if err != nil {
			panic(err)
		}
		for _, msg := range protocol.NewFromBytes(binData) {
			log.Debug(hex.EncodeToString(msg.Original))
			log.Infof("%s", msg.ShortForm())
		}
	},
}

func init() {
	decodeCmd.AddCommand(fileCmd)

	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// fileCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	// fileCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}
