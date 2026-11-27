package cmd

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	log "github.com/sirupsen/logrus"
)

//go:embed web/index.html
var webIndex []byte

// webServer is the optional web interface (web_listen). It reads the
// gateway state, and runs commands through the same handler as MQTT.
type webServer struct {
	m        *mqttClient
	listen   string
	password string
	logs     *logRing
	wifi     *wifiManager
	handler  http.Handler
}

// newWebServer serves on listen. Without auth there is no login at all;
// that has to be asked for explicitly (web_auth: false) when the address is
// reachable from the network.
func newWebServer(m *mqttClient, listen, password string, auth bool) (*webServer, error) {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return nil, fmt.Errorf("web_listen %q: want host:port, e.g. 127.0.0.1:8080 or :8080", listen)
	}
	if !auth {
		password = ""
		if !isLoopback(host) {
			log.Warnf("Web interface on %s without login (web_auth: false): anyone who can reach it controls the car", listen)
		}
	} else if password == "" && !isLoopback(host) {
		return nil, fmt.Errorf("web_listen %q is reachable from the network: set web_password, or web_auth: false to run without login", listen)
	}
	ws := &webServer{
		m:        m,
		listen:   listen,
		password: password,
		logs:     newLogRing(500),
		wifi:     m.wifi,
	}
	if ws.wifi == nil {
		ws.wifi = &wifiManager{iface: cfgString("wifi_interface")}
	}
	log.AddHook(ws.logs)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", ws.index)
	mux.HandleFunc("GET /api/state", ws.getState)
	mux.HandleFunc("GET /api/events", ws.events)
	mux.HandleFunc("POST /api/command", ws.command)
	mux.HandleFunc("GET /api/mqtt", ws.getMQTT)
	mux.HandleFunc("POST /api/mqtt", ws.setMQTT)
	mux.HandleFunc("POST /api/device_name", ws.setDeviceName)
	mux.HandleFunc("GET /api/wifi", ws.getWifi)
	mux.HandleFunc("POST /api/wifi/scan", ws.scanWifi)
	mux.HandleFunc("POST /api/wifi", ws.setWifi)
	ws.handler = ws.protect(mux)
	return ws, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (ws *webServer) serve() {
	log.Infof("Web interface on http://%s/", ws.listen)
	srv := &http.Server{
		Addr:              ws.listen,
		Handler:           ws.handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// No WriteTimeout: /api/events streams for as long as the page is open.
	}
	if err := srv.ListenAndServe(); err != nil {
		log.Errorf("Web interface stopped: %v", err)
	}
}

// protect asks for the password and blocks cross-site requests: a browser
// sends basic auth credentials along with requests from any site, but it
// cannot add a custom header to them without CORS, which is never allowed.
func (ws *webServer) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ws.password != "" {
			_, pw, ok := r.BasicAuth()
			if !ok || subtle.ConstantTimeCompare([]byte(pw), []byte(ws.password)) != 1 {
				if ok {
					time.Sleep(time.Second) // Slows down guessing.
				}
				w.Header().Set("WWW-Authenticate", `Basic realm="phev2mqtt", charset="UTF-8"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		if r.Method != http.MethodGet && r.Header.Get("X-Requested-With") != "phev2mqtt" {
			http.Error(w, "missing X-Requested-With header", http.StatusForbidden)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy",
			"default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; img-src data:; form-action 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	// An error here means the client is gone; nothing left to tell it.
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func readJSON(w http.ResponseWriter, r *http.Request, v interface{}) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func (ws *webServer) index(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(webIndex)
}

type webState struct {
	stateSnapshot
	MQTTConnected bool   `json:"mqtt_connected"`
	MQTTServer    string `json:"mqtt_server"`
	Version       string `json:"version"` // The release.
	Build         string `json:"build"`
	// climate_guard is on: show its last result, also before the first.
	ClimateGuard bool `json:"climate_guard"`
	// extra_commands is on: show its buttons and their last result.
	ExtraCommands bool `json:"extra_commands"`
}

func (ws *webServer) currentState() webState {
	return webState{
		stateSnapshot: ws.m.state.snapshot(),
		MQTTConnected: ws.m.mqttConnected(),
		MQTTServer:    cfgString("mqtt_server"),
		Version:       releaseVersion(),
		Build:         Version,
		ClimateGuard:  ws.m.climateGuard,
		ExtraCommands: ws.m.extraCommands,
	}
}

func (ws *webServer) getState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ws.currentState())
}

// events streams the state and new log lines as Server-Sent Events, at
// most a few times a second.
func (ws *webServer) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	var logSeq int64
	for {
		stateChanged, logChanged := ws.m.state.changes(), ws.logs.changes()
		state, _ := json.Marshal(ws.currentState())
		// A failing write means the browser is gone.
		if _, err := fmt.Fprintf(w, "event: state\ndata: %s\n\n", state); err != nil {
			return
		}
		if lines := ws.logs.since(logSeq); len(lines) > 0 {
			logSeq = lines[len(lines)-1].Seq
			data, _ := json.Marshal(lines)
			if _, err := fmt.Fprintf(w, "event: log\ndata: %s\n\n", data); err != nil {
				return
			}
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-stateChanged:
		case <-logChanged:
		case <-time.After(15 * time.Second): // Also notices MQTT reconnects.
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(250 * time.Millisecond): // Coalesce bursts.
		}
	}
}

// webCommand maps a button to the MQTT topic (after the prefix) it acts
// like, with the payloads it accepts. Registers cannot be set from here.
type webCommand struct {
	topic    string
	payloads []string
}

var webCommands = map[string]webCommand{
	"headlights":         {"/set/headlights", []string{"on", "off"}},
	"parkinglights":      {"/set/parkinglights", []string{"on", "off"}},
	"cancelchargetimer":  {"/set/cancelchargetimer", []string{"cancel"}},
	"climate_cool":       {"/set/climate/cool", []string{"10", "20", "30"}},
	"climate_heat":       {"/set/climate/heat", []string{"10", "20", "30"}},
	"climate_windscreen": {"/set/climate/windscreen", []string{"10", "20", "30"}},
	"climate_off":        {"/set/climate/mode", []string{"off"}},
	"climate_reset":      {"/set/climate/state", []string{"reset"}},
	"sync_clock":         {"/set/sync_clock", []string{""}},
	"connection":         {"/connection", []string{"on", "off", "restart"}},
}

func (ws *webServer) command(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Command string `json:"command"`
		Value   string `json:"value"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	switch req.Command {
	case "refresh":
		phev := ws.m.state.car()
		if phev == nil || !ws.m.state.isConnected() {
			writeError(w, http.StatusConflict, errors.New("not connected to the car"))
			return
		}
		go requestUpdate(phev)
	case "restart":
		log.Info("Web interface: restart")
		go func() {
			// Lets this answer reach the browser first.
			time.Sleep(500 * time.Millisecond)
			ws.m.restart()
		}()
	case "wifi_restart":
		go func() {
			if err := runWifiRestart(); err != nil {
				log.Errorf("Error restarting wifi: %v", err)
			}
		}()
	default:
		c, ok := webCommands[req.Command]
		if !ok {
			writeError(w, http.StatusBadRequest, fmt.Errorf("unknown command %q", req.Command))
			return
		}
		if !contains(c.payloads, req.Value) {
			writeError(w, http.StatusBadRequest, fmt.Errorf("%s: value must be one of %s", req.Command, strings.Join(c.payloads, ", ")))
			return
		}
		log.Infof("Web interface: %q %q", req.Command, req.Value)
		// SetRegister waits up to 10s for the car; the result shows in the log.
		go ws.m.safeHandleIncomingMqtt(nil, fakeMessage{topic: ws.m.topic(c.topic), payload: req.Value})
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "sent"})
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// fakeMessage lets the web interface run a command through the MQTT handler.
type fakeMessage struct {
	topic   string
	payload string
}

func (f fakeMessage) Duplicate() bool   { return false }
func (f fakeMessage) Qos() byte         { return 0 }
func (f fakeMessage) Retained() bool    { return false }
func (f fakeMessage) Topic() string     { return f.topic }
func (f fakeMessage) MessageID() uint16 { return 0 }
func (f fakeMessage) Payload() []byte   { return []byte(f.payload) }
func (f fakeMessage) Ack()              {}

type mqttSettings struct {
	Server            string `json:"server"`
	Username          string `json:"username"`
	PasswordSet       bool   `json:"password_set"`
	TopicPrefix       string `json:"topic_prefix"`
	HADiscovery       bool   `json:"ha_discovery"`
	HADiscoveryPrefix string `json:"ha_discovery_prefix"`
	DeviceName        string `json:"device_name"`
	Connected         bool   `json:"connected"`
	SettingsFile      string `json:"settings_file"`
}

// getMQTT never returns the password itself, only whether one is set.
func (ws *webServer) getMQTT(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, mqttSettings{
		Server:            cfgString("mqtt_server"),
		Username:          cfgString("mqtt_username"),
		PasswordSet:       cfgString("mqtt_password") != "",
		TopicPrefix:       cfgString("mqtt_topic_prefix"),
		HADiscovery:       cfgBool("ha_discovery"),
		HADiscoveryPrefix: cfgString("ha_discovery_prefix"),
		DeviceName:        cfgString("ha_device_name"),
		Connected:         ws.m.mqttConnected(),
		SettingsFile:      webSettingsFile(),
	})
}

func (ws *webServer) setMQTT(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Server            string  `json:"server"`
		Username          string  `json:"username"`
		Password          *string `json:"password"` // Absent: keep the current one.
		TopicPrefix       string  `json:"topic_prefix"`
		HADiscovery       bool    `json:"ha_discovery"`
		HADiscoveryPrefix string  `json:"ha_discovery_prefix"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := validateBrokerURL(req.Server); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(req.TopicPrefix) == "" || strings.ContainsAny(req.TopicPrefix, "#+") ||
		strings.TrimSpace(req.HADiscoveryPrefix) == "" || strings.ContainsAny(req.HADiscoveryPrefix, "#+") {
		writeError(w, http.StatusBadRequest, errors.New("topic prefixes must be non-empty and without # or +"))
		return
	}
	update := map[string]interface{}{
		"mqtt_server":         req.Server,
		"mqtt_username":       req.Username,
		"mqtt_topic_prefix":   req.TopicPrefix,
		"ha_discovery":        req.HADiscovery,
		"ha_discovery_prefix": req.HADiscoveryPrefix,
	}
	if req.Password != nil {
		update["mqtt_password"] = *req.Password
	}
	if err := saveWebSettings(update); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	log.Infof("Web interface: MQTT settings changed, reconnecting to %q", req.Server)
	go ws.m.connectMQTT()
	ws.getMQTT(w, r)
}

// setDeviceName renames the car in Home Assistant, without reconnecting.
// Home Assistant keeps a name given in its own interface over this one.
func (ws *webServer) setDeviceName(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	name := strings.TrimSpace(req.Name)
	if len([]rune(name)) > 64 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		writeError(w, http.StatusBadRequest, errors.New("the name must be at most 64 characters, on one line"))
		return
	}
	if err := saveWebSettings(map[string]interface{}{"ha_device_name": name}); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	log.Infof("Web interface: name in Home Assistant now %q", deviceName(name, ws.m.state.getVIN()))
	published := ws.m.renameDevice(name)
	writeJSON(w, http.StatusOK, map[string]interface{}{"name": name, "published": published})
}

func validateBrokerURL(s string) error {
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return fmt.Errorf("broker %q: want e.g. tcp://192.168.1.10:1883", s)
	}
	switch u.Scheme {
	case "tcp", "mqtt", "ssl", "tls", "mqtts", "ws", "wss":
		return nil
	}
	return fmt.Errorf("broker %q: unsupported scheme %q", s, u.Scheme)
}

func (ws *webServer) getWifi(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ws.wifi.status())
}

func (ws *webServer) scanWifi(w http.ResponseWriter, r *http.Request) {
	networks, err := ws.wifi.scan()
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, networks)
}

func (ws *webServer) setWifi(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SSID      string `json:"ssid"`
		Password  string `json:"password"`
		ClonedMAC string `json:"cloned_mac"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	log.Infof("Web interface: connecting %s to %q", ws.wifi.iface, req.SSID)
	if err := ws.wifi.connectCar(req.SSID, req.Password, strings.TrimSpace(req.ClonedMAC)); err != nil {
		log.Errorf("WiFi: %q", err.Error())
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, ws.wifi.status())
}
