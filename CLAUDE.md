# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

phev2mqtt is a Go gateway between a Mitsubishi Outlander PHEV / Eclipse Cross (over the car's own WiFi remote-control protocol, TCP to `192.168.8.46:8080`) and MQTT, with Home Assistant discovery and an optional web interface. This repo (`athomasse/phev2mqtt-nxt`) is a fork of `buxtronix/phev2mqtt` (git remote `upstream`); the Go module path is still `github.com/buxtronix/phev2mqtt`.

## Commands

```sh
go build                      # ./phev2mqtt; pure Go, no cgo
go build -tags pcap           # adds `decode pcap` (cmd/pcap.go), needs libpcap-dev
go vet ./...                  # CI runs this
go test -race ./...           # CI runs this
golangci-lint run --build-tags pcap ./...   # CI runs this (.golangci.yml: gofmt + standard linters)
go test -race ./cmd -run TestName   # a single test
packaging/build.sh 0.4.0      # cross-compiled binaries + .debs into dist/ (needs nfpm on PATH)
./phev2mqtt emulator          # fake car to point a client at
./phev2mqtt client mqtt --config=/dev/null --mqtt_server tcp://host:1883 -v debug
```

CI (`.github/workflows/build.yaml`) vets, tests, builds linux amd64/arm64/armv6/386 with `CGO_ENABLED=0` (the `pcap` tag only gets a separate vet and build, with libpcap-dev), smoke-tests the amd64 `.deb` including its maintainer scripts, and publishes a GitHub release with build provenance attestations (from `SHA256SUMS`) on a `v*` tag. `lint.yaml` runs golangci-lint (v2, `.golangci.yml`): code must be gofmt-formatted and lint-clean; errors are checked unless the call is in the errcheck exclusions there. Two versions are injected with `-ldflags -X`: `cmd.Version` (the Debian version, `0.0.0~git<time>.<hash>` for untagged builds, shown by `--version`) and `cmd.Release` (the last `v*` tag without the `v`, shown in the web interface so it matches GitHub releases; CI checks out with `fetch-depth: 0` for this).

## Architecture

- **`protocol/`** – wire format and register decoding. Packets are `[type][len][ack][data...][checksum]`, XOR-obfuscated with a rolling key (`SecurityKey` in `raw.go`). `PhevMessage` decodes into a typed `Register` (`message.go`) chosen by register number. `protocol/README.md` documents the reverse-engineered protocol and register meanings; keep it in sync when decoding changes.
- **`client/`** – one TCP connection to the car. `Connect` starts `reader`, `writer`, `manage` and `pinger` goroutines; `Start` waits for the car's start request, which also sets `ModelYear` (MY14/MY18/MY24 use different start messages and, for climate, different registers). Incoming messages go both to the `Recv` channel and to every `Listener` (fan-out, used by `SetRegister` to wait for its ack and to retry with the car's XOR on `CmdInBadEncoding`). Always send through `SendMessage` (never block on `Send` after close) and `Close` a client when done: the car allows a single client, and a lingering connection blocks reconnects.
- **`emulator/`** – a fake car speaking the same protocol, reusing `client.Listener`.
- **`cmd/`** – cobra commands; configuration is viper (flags bound to keys, `--config` YAML whose keys equal flag names, `--config=/dev/null` for none; an unreadable `--config` is an error). The main command is `client mqtt` (`mqtt.go`):
  - `mqttClient.Run` loops forever: `handlePhev` creates a fresh `client.Client` per connection attempt, publishes registers (raw under `phev/register/xx` plus decoded topics), acks each register update, and periodically requests a full update. Around it: `available` online/offline, optional WiFi-interface restart (`wifi_restart_*`), and `wifiManager.reconnectCar`: after the car's WiFi has been down for 2 minutes, at most every 2 minutes and in the background, `nmcli connection up phev2mqtt-car` when the device is idle (`30 (disconnected)`) and the car's SSID is in NetworkManager's own scan results (`--rescan no`). NetworkManager never autoconnects a profile again after a `no-secrets` failure, which is what the car driving off mid-handshake causes. Off without `nmcli` on PATH.
  - `handleIncomingMqtt` handles `phev/set/...` topics. The web interface sends its commands through this same handler (as a `fakeMessage`), so MQTT and web share one command path.
  - `gatewayState` (`state.go`) holds the current car client, connection state, VIN, model year and last published values behind a mutex; `changes()` returns a channel closed on every change, which drives the web UI's server-sent events (`/api/events`).
  - Read config with `cfgString`/`cfgBool`/`cfgDuration` (`settings.go`), not viper directly: the web interface can change MQTT settings at runtime, which are saved to a separate `settings.yaml` (`STATE_DIRECTORY`, i.e. `/var/lib/phev2mqtt` under systemd) and merged over the config file under `configMu`.
  - The car's WiFi state comes from netlink link events (`wifilink_linux.go`) and the signal from `/proc/net/wireless`, read only on those events and on the existing `update_interval` tick: new status features must not add polling or timers. Published retained under `phev/wifi/` and `phev/connection/`.
  - Restart (`phev/restart`, web button, HA button) re-execs the binary in place (`restart_unix.go`) after closing the car connection; retained `phev/restart` messages are ignored to avoid restart loops.
  - HA discovery entity names carry no device prefix (HA prepends the device name); the device name is `ha_device_name` (default `PHEV <VIN>`), renamable from the web interface without reconnecting. Keep `unique_id`s stable when renaming entities.
  - Web interface (`web.go`, single embedded `web/index.html`, `weblog.go` log ring buffer, `wifi.go` NetworkManager via `nmcli`) is off unless `web_listen` is set. It refuses a non-loopback address without `web_password` unless `web_auth: false`; `web_password` and `web_auth` are config-file-only on purpose (not flags, so the password stays out of the process list). Non-GET requests must carry `X-Requested-With: phev2mqtt` (CSRF guard). Raw register setting is deliberately not exposed in the web UI. The README screenshots (`docs/web-interface-{light,dark}.png`) come from `docs/screenshot-server.py`, which serves `index.html` with made-up data; never screenshot the real car (its VIN and SSID).
  - Optional features ported from corautem/phev2mqtt (PHEV Link code from before it went proprietary; keep the credits in README and file headers), all off by default: `extra_sensors` (`protocol.StatusTracker`, `cmd/mqtt_extra.go`; entities are removed with empty discovery configs when off, retired ones always), `key_resync` (`client.KeyResyncOption`, `SecurityKey.ResyncSend`), `climate_guard` (`cmd/climate_guard.go`: echoes the car's 0x22/0x19 on MY14, refuses while switched on or quick charging, confirms only from registers sent after the command, result in `phev/command/result`), `extra_commands` (`cmd/commands.go`: Sync Car Clock writes 0x05 and checks the clock the car reports afterwards; its HA buttons are `extraSensor`s with component `button`).
- **`packaging/`** – nfpm `.deb`: hardened systemd unit running as user `phev2mqtt`, `/etc/phev2mqtt/config.yaml` (root:phev2mqtt 640, a conffile), polkit rule letting only that service manage NetworkManager WiFi. The service cannot `sudo`; it may run `ip link` (default `wifi_restart_command`). Keep `packaging/config.yaml` comments in sync with new flags.
- **`addon/`** – legacy Home Assistant add-on that clones and builds upstream, not this repo.

When adding MQTT topics or config keys, also update the README tables/config examples and, for entities, `publishHomeAssistantDiscovery`.
