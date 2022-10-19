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
	"fmt"
	"github.com/buxtronix/phev2mqtt/emulator"
	"github.com/spf13/cobra"
	"strings"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	log "github.com/sirupsen/logrus"
)

// emulatorCmd represents the emulate command
var emulatorCmd = &cobra.Command{
	Use:   "emulator",
	Short: "Emulate a car, to enable app testing",
	Long: `Starts up a service that emulates a car.

If --mqtt_server is specified, it will connect to the given
MQTT server, and allow you to send registers to the client,
at topic phev/emu/set/register/<reg>
	`,
	RunE: func(cmd *cobra.Command, args []string) error {
		emu := &emu{}
		return emu.Run(cmd, args)
	},
}

type emu struct {
	client   mqtt.Client
	options  *mqtt.ClientOptions
	mqttData map[string]string

	car    *emulator.Car
	prefix string
}

func (e *emu) topic(topic string) string {
	return fmt.Sprintf("%s%s", e.prefix, topic)
}

func (e *emu) Run(cmd *cobra.Command, args []string) error {

	mqttServer, _ := cmd.Flags().GetString("mqtt_server")
	mqttUsername, _ := cmd.Flags().GetString("mqtt_username")
	mqttPassword, _ := cmd.Flags().GetString("mqtt_password")
	e.prefix, _ = cmd.Flags().GetString("mqtt_topic_prefix")

	if mqttServer != "" {
		e.options = mqtt.NewClientOptions().
			AddBroker(mqttServer).
			SetClientID("phev2mqtt_emu").
			SetUsername(mqttUsername).
			SetPassword(mqttPassword).
			SetAutoReconnect(true).
			SetDefaultPublishHandler(e.handleIncomingMqtt).
			SetWill(e.topic("/available"), "offline", 0, true)

		e.client = mqtt.NewClient(e.options)
		if token := e.client.Connect(); token.Wait() && token.Error() != nil {
			return token.Error()
		}
		if token := e.client.Subscribe(e.topic("/set/#"), 0, nil); token.Wait() && token.Error() != nil {
			return token.Error()
		}

	}
	e.mqttData = map[string]string{}

	return e.manageCar(cmd)
}

func (e *emu) publish(topic, payload string) {
	if e.client == nil {
		return
	}
	if cache := e.mqttData[topic]; cache != payload {
		e.client.Publish(e.topic(topic), 0, false, payload)
		e.mqttData[topic] = payload
	}
}

func (e *emu) handleIncomingMqtt(client mqtt.Client, msg mqtt.Message) {
	topicParts := strings.Split(msg.Topic(), "/")
	log.Infof("Topic got=%s want=%s", msg.Topic(), e.topic("/set/register/"))
	switch {
	case strings.HasPrefix(msg.Topic(), e.topic("/set/register/")):
		if len(topicParts) != 5 {
			log.Infof("Bad topic format [%s]", msg.Topic())
			return
		}
		register, err := hex.DecodeString(topicParts[4])
		if err != nil {
			log.Infof("Bad register in topic [%s]: %v", msg.Topic(), err)
			return
		}
		data, err := hex.DecodeString(string(msg.Payload()))
		if err != nil {
			log.Infof("Bad payload [%s]: %v", msg.Payload(), err)
			return
		}
		if err := e.car.SetRegister(register[0], data); err != nil {
			log.Infof("Error setting register %02x: %v", register[0], err)
			return
		}
	}
}

func (e *emu) manageCar(cmd *cobra.Command) error {
	var err error
	address, _ := cmd.Flags().GetString("address")
	e.car, err = emulator.NewCar(emulator.AddressOption(address))
	if err != nil {
		return err
	}
	if err := e.car.Begin(); err != nil {
		return err
	}
	e.publish("/available", "online")
	select {}
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
	emulatorCmd.Flags().String("mqtt_server", "tcp://127.0.0.1:1883", "Address of MQTT server")
	emulatorCmd.Flags().String("mqtt_username", "", "Username to login to MQTT server")
	emulatorCmd.Flags().String("mqtt_password", "", "Password to login to MQTT server")
	emulatorCmd.Flags().String("mqtt_topic_prefix", "phev/emu", "Prefix for MQTT topics")
}
