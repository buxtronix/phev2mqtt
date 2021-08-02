#!/usr/bin/with-contenv bashio

CONFIG_PATH=/data/options.json

export mqtt_server="$(bashio::config 'mqtt_server')"
export mqtt_user="$(bashio::config 'mqtt_user')"
export mqtt_password="$(bashio::config 'mqtt_password')"
export debug="$(bashio::config 'debug')"

if [[ $debug == "true" ]]
then
	echo Debug mode on - sleeping indefinitely
	sleep inf
fi

echo Starting phev2mqtt

/opt/phev2mqtt \
        client \
        mqtt \
        --mqtt_server "tcp://${mqtt_server}/" \
        --mqtt_username "${mqtt_user}" \
        --mqtt_password "${mqtt_password}"

