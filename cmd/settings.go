package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/spf13/viper"
	"go.yaml.in/yaml/v3"
)

// viper is not safe for concurrent use, and the web interface changes
// settings while the gateway runs: every access after startup goes through
// these helpers.
var configMu sync.RWMutex

func cfgString(key string) string {
	configMu.RLock()
	defer configMu.RUnlock()
	return viper.GetString(key)
}

func cfgBool(key string) bool {
	configMu.RLock()
	defer configMu.RUnlock()
	return viper.GetBool(key)
}

func cfgDuration(key string) time.Duration {
	configMu.RLock()
	defer configMu.RUnlock()
	return viper.GetDuration(key)
}

// The settings the web interface may change. They are stored apart from the
// config file, which belongs to the administrator (or Ansible), and override
// it; command line flags still win over both.
var webSettingKeys = []string{
	"mqtt_server",
	"mqtt_username",
	"mqtt_password",
	"mqtt_topic_prefix",
	"ha_discovery",
	"ha_discovery_prefix",
	"ha_device_name",
}

// webSettingsFile is web_settings_file, else settings.yaml in the systemd
// StateDirectory= (/var/lib/phev2mqtt), else in the user's config directory.
func webSettingsFile() string {
	if f := cfgString("web_settings_file"); f != "" {
		return f
	}
	if dir := os.Getenv("STATE_DIRECTORY"); dir != "" {
		return filepath.Join(dir, "settings.yaml")
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "phev2mqtt", "settings.yaml")
	}
	return ""
}

func readWebSettings(file string) (map[string]interface{}, error) {
	settings := map[string]interface{}{}
	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading web settings: %w", err)
	}
	if err := yaml.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("parsing web settings %s: %w", file, err)
	}
	known := map[string]interface{}{}
	for _, k := range webSettingKeys {
		if v, ok := settings[k]; ok {
			known[k] = v
		}
	}
	return known, nil
}

// loadWebSettings merges the saved web settings into the configuration.
func loadWebSettings() error {
	file := webSettingsFile()
	if file == "" {
		return nil
	}
	settings, err := readWebSettings(file)
	if err != nil || len(settings) == 0 {
		return err
	}
	configMu.Lock()
	defer configMu.Unlock()
	return viper.MergeConfigMap(settings)
}

// saveWebSettings stores update on top of the saved web settings (the file
// holds the MQTT password, so only its owner may read it) and applies them.
func saveWebSettings(update map[string]interface{}) error {
	file := webSettingsFile()
	if file == "" {
		return errors.New("no place to store settings: set web_settings_file")
	}
	settings, err := readWebSettings(file)
	if err != nil {
		return err
	}
	for k, v := range update {
		settings[k] = v
	}
	data, err := yaml.Marshal(settings)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, file); err != nil {
		return err
	}
	configMu.Lock()
	defer configMu.Unlock()
	return viper.MergeConfigMap(settings)
}
