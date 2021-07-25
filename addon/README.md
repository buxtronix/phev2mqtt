This requires that the local wifi is already connected to the Outlander, you should be able to check this by ensuring that you have a 192.168.8.x IP address

Once installed as a local addon, you can configure the mqtt target and username/password and then start and stop phev2mqtt from the GUI

These files need to go in a directory (called phev for example) in the local addons.

If you ssh into the main os, this will be in /usr/share/hassio/addons/local/phev
If you ssh in using the terminal add on, this will be /addons/phev (or symblink /root/addons/phev)
If you connect to the hassio_supervisort docker container, this will be /data/addons/local/phev

More information about local addons at https://developers.home-assistant.io/docs/add-ons/tutorial/

If you set the debug flag on, it will just start the container and sleep indefinitely, you can then run the phev2mqtt manually, to run "phev2mqtt client watch" for example.

NOTE - on 32bit Raspberry Pi there's an issue with the latest alpine docker images and old versions of libseccomp (including the ones in Raspbian repos), this will stop the Dockerfile building. In order to overcome this, you may need to manually install a newer version with

    wget http://ftp.us.debian.org/debian/pool/main/libs/libseccomp/libseccomp2_2.4.4-1~bpo10+1_armhf.deb
    dpkg -i libseccomp2_2.4.4-1~bpo10+1_armhf.deb
