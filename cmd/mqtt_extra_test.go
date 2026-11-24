package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestExtraSensorDiscovery(t *testing.T) {
	data := map[string]string{}
	addExtraSensorDiscovery(data, true)
	if len(data) != len(extraSensors) {
		t.Fatalf("%d discovery topics for %d sensors", len(data), len(extraSensors))
	}
	ids := map[string]bool{}
	for topic, config := range data {
		topic = fmt.Sprintf(topic, "homeassistant", "VIN1")
		for in, out := range map[string]string{"__DEVICE__": "PHEV VIN1", "__VIN__": "VIN1", "__TOPIC__": "phev"} {
			config = strings.ReplaceAll(config, in, out)
		}
		var c map[string]interface{}
		if err := json.Unmarshal([]byte(config), &c); err != nil {
			t.Fatalf("%s: %v\n%s", topic, err, config)
		}
		id, _ := c["unique_id"].(string)
		if !strings.HasPrefix(id, "VIN1_") || ids[id] {
			t.Errorf("%s: unique_id %q missing, wrong or duplicate", topic, id)
		}
		ids[id] = true
		if want := "homeassistant/" + strings.Split(topic, "/")[1] + "/" + id + "/config"; topic != want {
			t.Errorf("topic %s, want %s", topic, want)
		}
		if strings.HasPrefix(topic, "homeassistant/binary_sensor/") && c["payload_on"] != "on" {
			t.Errorf("%s: payload_on %v", topic, c["payload_on"])
		}
	}
}

func TestExtraSensorDiscoveryDisabled(t *testing.T) {
	data := map[string]string{}
	addExtraSensorDiscovery(data, false)
	for topic, config := range data {
		if config != "" {
			t.Errorf("%s: config %q with extra_sensors off, want empty to remove it", topic, config)
		}
	}
}
