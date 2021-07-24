#!/usr/bin/with-contenv bashio

CONFIG_PATH=/data/options.json

export mqtt_server="$(bashio::config 'mqtt_server')"
export mqtt_user="$(bashio::config 'mqtt_user')"
export mqtt_password="$(bashio::config 'mqtt_password')"

/tmp/phev2mqtt/phev2mqtt \
        client \
        mqtt \
        --mqtt_server "tcp://${mqtt_server}/" \
        --mqtt_username "${mqtt_user}" \
        --mqtt_password "${mqtt_password}"

