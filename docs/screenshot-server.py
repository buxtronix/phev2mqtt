"""Serves the web interface with made-up data for the README screenshots.

Usage: python3 -I docs/screenshot-server.py cmd/web/index.html 8731
"/" is the light page, "/dark" the dark one. Then, for light and dark:

  chromium --headless=new --hide-scrollbars --window-size=1280,1945 \
    --screenshot=docs/web-interface-light.png http://127.0.0.1:8731/
"""
import json, sys, time
from datetime import datetime, timedelta, timezone
from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler

INDEX, PORT = sys.argv[1], int(sys.argv[2])
now = datetime.now(timezone.utc).astimezone()
iso = lambda d: d.isoformat()
local = lambda d: d.strftime("%Y-%m-%d %H:%M")
cmd_at = now + timedelta(minutes=-2, seconds=10 * 3)  # the "Command:" log line

STATE = {
    "enabled": True, "car_connected": True, "available": True,
    "last_connect": iso(now - timedelta(minutes=42)), "updated": iso(now - timedelta(minutes=3)),
    "vin": "DEMO0PHEV0000001", "model_year": "MY14",
    "values": {
        "/battery/level": "80", "/charge/charging": "on", "/charge/type": "normal",
        "/charge/remaining": "90", "/charge/plug": "connected",
        "/power/switch": "off", "/car/clock_offset": "0", "/climate/state": "heat",
        "/door/locked": "closed", "/door/driver": "closed", "/door/front_passenger": "closed",
        "/door/rear_left": "closed", "/door/rear_right": "closed", "/door/bonnet": "closed", "/door/boot": "closed",
        "/lights/head": "off", "/lights/parking": "off", "/lights/interior": "off", "/lights/hazard": "off",
        "/wifi/state": "connected", "/wifi/signal": "-58",
        "/command/result": local(cmd_at) + " Heat 20 min: done, the climate is running",
    },
    "mqtt_connected": True, "mqtt_server": "tcp://192.168.1.10:1883",
    "version": "0.6.2", "build": "0.6.2", "climate_guard": True, "extra_commands": True,
}
LOG = [(-9, "info", "Using config file: /etc/phev2mqtt/config.yaml"),
       (-9, "info", "Connecting to MQTT broker tcp://192.168.1.10:1883"),
       (-9, "info", "Web interface on http://:8080/"),
       (-9, "info", "WiFi wlan0 connected"),
       (-9, "info", "Connected to MQTT broker"),
       (-8, "info", "%PHEV_TCP_CONNECTED%"),
       (-8, "info", "Car online: data from the car"),
       (-5, "info", "%PHEV_KEY_RESYNC% car expected key 0x5c, resynced: true"),
       (-2, "info", "Web interface: climate_heat 20"),
       (-2, "info", "Topic: [phev/set/climate/heat] Payload: [20]"),
       (-2, "info", "Command: " + local(cmd_at) + " Heat 20 min: done, the climate is running"),
       (-1, "error", "WiFi: the car refused the WiFi password for \"REMOTE1a2b3c\"; check it, letters like l, I and 1 or 0 and O look alike"),
       (-1, "info", "Web interface: connecting wlan0 to \"REMOTE1a2b3c\""),
       (-1, "info", "WiFi wlan0 connected")]
LINES = [{"seq": i + 1, "time": iso(now + timedelta(minutes=m, seconds=i * 3)), "level": lv, "message": msg}
         for i, (m, lv, msg) in enumerate(LOG)]
MQTT = {"server": "tcp://192.168.1.10:1883", "username": "phev", "password_set": True,
        "topic_prefix": "phev", "ha_discovery_prefix": "homeassistant", "ha_discovery": True,
        "device_name": "Outlander PHEV", "settings_file": "/var/lib/phev2mqtt/settings.yaml"}
WIFI = {"available": True, "interface": "wlan0", "state": "100 (connected)", "connection": "phev2mqtt-car",
        "address": "192.168.8.47/24", "car_configured": True, "car_ssid": "REMOTE1a2b3c", "car_cloned_mac": ""}

class H(BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def send(self, body, ctype):
        b = body.encode()
        self.send_response(200); self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(b))); self.end_headers(); self.wfile.write(b)
    def do_GET(self):
        if self.path in ("/", "/dark"):
            html = open(INDEX).read()
            if self.path == "/dark":
                html = html.replace('<html lang="en">', '<html lang="en" data-theme="dark">', 1)
            self.send(html, "text/html; charset=utf-8")
        elif self.path == "/api/mqtt": self.send(json.dumps(MQTT), "application/json")
        elif self.path == "/api/wifi": self.send(json.dumps(WIFI), "application/json")
        elif self.path == "/api/events":
            self.send_response(200); self.send_header("Content-Type", "text/event-stream"); self.end_headers()
            self.wfile.write(("event: state\ndata: %s\n\nevent: log\ndata: %s\n\n" % (json.dumps(STATE), json.dumps(LINES))).encode())
            self.wfile.flush()
            try:
                while True:
                    time.sleep(5); self.wfile.write(b": ping\n\n"); self.wfile.flush()
            except OSError:
                pass
        else:
            self.send_error(404)

ThreadingHTTPServer(("127.0.0.1", PORT), H).serve_forever()
