# Version 1.3
* Removed host networking as it wasn't required

# Version 1.2
* Removed fatal error if Outlander is not available

# Version 1.1
* Set inital values of mqtt_user and mqtt_password to null instead of dummy values to ensure that user enters them
* Added debug variable to allow start of container and then manual connection to it to run commands
* Added netcat test to check that Outlander is available
* Moved phev2mqtt binary from /tmp/phev2mqtt to /opt

# Version 1
Initial build
