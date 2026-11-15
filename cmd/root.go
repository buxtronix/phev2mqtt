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
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/wercker/journalhook"
	"os"
)

var (
	cfgFile   string
	logLevel  string
	logTimes  bool
	logSyslog bool
)

// Version is set at build time with
// -ldflags "-X github.com/buxtronix/phev2mqtt/cmd.Version=...".
var Version = "dev"

// Release is the GitHub release (its tag without the v) this build is, or
// builds on; set like Version. The web interface shows it.
var Release = ""

// releaseVersion is Release, or Version when the build did not set it.
func releaseVersion() string {
	if Release != "" {
		return Release
	}
	return Version
}

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:     "phev2mqtt",
	Version: Version,
	Short:   "A utility for communicating with a Mitsubishi PHEV",
	Long: `See below for subcommands. For further information
	on this tool, see https://github.com/buxtronix/phev2mqtt.`,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		level, err := log.ParseLevel(logLevel)
		if err != nil {
			panic(err)
		}
		log.SetLevel(level)
		if logSyslog {
			journalhook.Enable()
		}
		if logTimes {
			log.SetFormatter(&log.TextFormatter{
				FullTimestamp: true,
			})
		} else {
			log.SetFormatter(&log.TextFormatter{
				FullTimestamp:    false,
				DisableColors:    true,
				DisableTimestamp: true,
			})
		}
	},

	// Uncomment the following line if your bare application
	// has an action associated with it:
	// Run: func(cmd *cobra.Command, args []string) { },
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	cobra.CheckErr(rootCmd.Execute())
}

func init() {
	cobra.OnInitialize(initConfig)

	// Here you will define your flags and configuration settings.
	// Cobra supports persistent flags, which, if defined here,
	// will be global for your application.

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.phev2mqtt.yaml)")
	rootCmd.PersistentFlags().StringVarP(&logLevel, "verbosity", "v", "info", "logging level to use")
	rootCmd.PersistentFlags().BoolVarP(&logTimes, "log_timestamps", "t", false, "coloured logging with timestamps")
	rootCmd.PersistentFlags().BoolVarP(&logSyslog, "log_syslog", "s", false, "plain logging to syslog instead of console")

	// Cobra also supports local flags, which will only run
	// when this action is called directly.
	// rootCmd.Flags().BoolP("toggle", "t", false, "Help message for toggle")
}

// initConfig reads in config file and ENV variables if set.
func initConfig() {
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		// Search config in home directory with name ".phev2mqtt" (without extension).
		viper.AddConfigPath(home)
		viper.SetConfigType("yaml")
		viper.SetConfigName(".phev2mqtt")
	}

	viper.AutomaticEnv() // read in environment variables that match

	// If a config file is found, read it in. One given with --config must
	// be readable: silently falling back to the defaults (e.g. a local MQTT
	// broker) hides a typo or a permission problem. --config=/dev/null is
	// the documented way to read no config file at all.
	if err := viper.ReadInConfig(); err == nil {
		fmt.Fprintln(os.Stderr, "Using config file:", viper.ConfigFileUsed())
	} else if cfgFile != "" && cfgFile != os.DevNull {
		cobra.CheckErr(fmt.Errorf("reading config file: %w", err))
	}
}
