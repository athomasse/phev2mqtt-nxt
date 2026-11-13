# phev2mqtt-nxt - Mitsubishi Outlander PHEV to MQTT gateway

A gateway between a Mitsubishi Outlander PHEV (or Eclipse Cross PHEV) and MQTT, using the
car's own WiFi remote control protocol, with Home Assistant discovery and an optional web
interface.

This is a fork of [buxtronix/phev2mqtt](https://github.com/buxtronix/phev2mqtt) by Ben
Buxton, which did all the hard work of reverse engineering the protocol. The fork adds
Debian packages, a web interface, connection and WiFi status, and a number of fixes; see
[What this fork adds](#what-this-fork-adds) and [Credits](#credits).

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="docs/web-interface-dark.png">
  <img alt="The phev2mqtt web interface: car status, controls, MQTT and WiFi settings and the log" src="docs/web-interface-light.png" width="800">
</picture>

*The web interface (with made-up data).*

## Features

 * MQTT gateway to the car: battery, charging, plug, doors, locks, lights and climate,
   as raw registers and as decoded values
 * Commands from MQTT: climate (cool, heat, windscreen) for 10, 20 or 30 minutes, head and
   parking lights, cancelling the charge timer, (re)connecting
 * Home Assistant MQTT discovery, with the car as a device
 * Connection and WiFi status: whether the car is connected, the WiFi signal, and when it
   last connected and disconnected
 * An optional web interface for status, commands, MQTT and WiFi setup and the log
 * Registering with the car, near-instant response to commands
 * Debian packages for every Raspberry Pi and for PCs, with a hardened systemd service
 * Debugging tools: live message watching, PCAP decoding and a vehicle emulator

Tested on an Outlander PHEV MY18, an MY14 and an Eclipse Cross (2022).

## What this fork adds

Compared to [buxtronix/phev2mqtt](https://github.com/buxtronix/phev2mqtt):

 * **Packages**: `.deb` packages and plain binaries for `armhf` (every 32-bit Raspberry
   Pi, including the B+ and Zero), `arm64`, `amd64` and `i386`, with a systemd service
   that runs as its own user, a polkit rule for managing the car's WiFi, and a commented
   config file.
 * **Web interface**: car status, climate, lights and charge timer buttons, reconnecting
   and restarting, MQTT settings, WiFi setup through NetworkManager, renaming the car in
   Home Assistant, and a live log with *Errors only* and *Clear*. The version shown
   matches the GitHub release.
 * **Connection and WiFi status** over MQTT and in Home Assistant, from kernel events: no
   extra polling and nothing extra sent to the car.
 * **Restart** from MQTT, Home Assistant or the web interface.
 * **Home Assistant**: a configurable device name, entity names without a repeated
   prefix, and a connection sensor that stays available when the car is away.
 * **Fixes**: connection leaks and deadlocks that blocked reconnecting, MQTT
   subscriptions and discovery after a broker restart, crashes and wrong commands from
   MQTT input, retained discovery, clear WiFi error messages.
 * **CI**: tests with the race detector, `go vet`, govulncheck, CodeQL, dependency review
   and Dependabot.

## Getting started

### Install the package (Debian, Ubuntu, Raspberry Pi OS)

Each [release](https://github.com/athomasse/phev2mqtt-nxt/releases) has a `.deb` for
`armhf`, `arm64`, `amd64` and `i386`, plus plain binaries and a `SHA256SUMS` file. The
package installs the binary, a hardened systemd service running as its own `phev2mqtt`
user, and a commented config file in `/etc/phev2mqtt/config.yaml` that upgrades leave
alone.

```
version=0.4.0
deb=phev2mqtt_${version}_$(dpkg --print-architecture).deb
base=https://github.com/athomasse/phev2mqtt-nxt/releases/download/v$version
curl -fLO "$base/SHA256SUMS" -fLO "$base/$deb"
sha256sum -c --ignore-missing SHA256SUMS
sudo apt install "./$deb"
```

Then:

1. Set up the WiFi connection to the car, see [Connecting to the car](#connecting-to-the-car)
   and the example files in `/usr/share/doc/phev2mqtt/examples/`. With NetworkManager
   you can also do this in the [web interface](#web-interface).
2. Register with the car once: `phev2mqtt client register`.
3. Fill in the MQTT broker: `sudoedit /etc/phev2mqtt/config.yaml`.
4. Start the gateway: `sudo systemctl enable --now phev2mqtt`, and follow its log with
   `journalctl -fu phev2mqtt`.

The service cannot use `sudo`; it may run `ip link` itself, which is all the default
`wifi_restart_command` in the config file needs.

### Build from source

 * Install [Go](https://go.dev/dl/) 1.26 or newer. Your distribution's package may be
   too old; for a Raspberry Pi 1 or Zero choose the ARMv6 release.
 * Run `go build` in the top level directory, and check it with `./phev2mqtt -h`.
 * For `decode pcap` (see [Development](#development)), build with `go build -tags pcap`;
   this needs the libpcap development package (`libpcap-dev`).
 * `packaging/build.sh <version>` builds all release binaries and packages into `dist/`;
   it needs [nfpm](https://nfpm.goreleaser.com).

## Connecting to the car

### WiFi

The car runs a WiFi network with an SSID like *REMOTExxxxxx*; its name and password come
with the car (see the
[Mitsubishi instructions](https://www.mitsubishi-motors.com/en/products/outlander_phev/app/remote/)).
The car is always `192.168.8.46` and gives its client `192.168.8.47`.

Connect the machine running phev2mqtt to it, and make sure the car's network never
becomes the default route, so MQTT and the internet keep going over your own network.
See [Raspberry Pi setup](#raspberry-pi-setup) for NetworkManager and systemd-networkd
examples.

If connecting fails with *the car refused the WiFi password*, check the password:
letters like `l`, `I` and `1` or `0` and `O` look alike on the card. The signal matters
too; a weak one (around -75 dBm or lower) causes dropouts.

### Register with the car

Put the car into registration mode ("Setup Your Vehicle" in the Mitsubishi
instructions; you may need to reconnect the WiFi), then within 5 minutes run:

```
phev2mqtt client register
```

You should shortly see a message that the registration succeeded. The car registers the
MAC address that connects to it, so register from the machine (and WiFi adapter) that
will run the gateway, with its own MAC address rather than a cloned one of your phone.
`phev2mqtt client unregister` removes it again.

The car accepts one client at a time: disconnect the phone app while phev2mqtt runs.

### Testing the connection

`phev2mqtt client watch` shows the messages from the car as they arrive; some of them are
decoded into readable values such as charge and climate status. Use `-v debug` or
`-v trace` for more detail.

## MQTT gateway

The main feature is the gateway between the car and MQTT. The car's registers are
published to MQTT, both raw and decoded, and commands on MQTT topics control the car.

```
phev2mqtt client mqtt --mqtt_server tcp://<broker>:1883/ [--mqtt_username <user>] [--mqtt_password <password>]
```

With the package, the service runs this with `/etc/phev2mqtt/config.yaml`.

### Published topics

| Topic | Description |
|---|---|
| phev/register/[register] | Raw value of each register, as a hex string |
| phev/available | Connection to the car: *online* or *offline* (retained) |
| phev/connection/last_connected | When the connection to the car was last made (RFC 3339, retained) |
| phev/connection/last_disconnected | When the connection to the car was last lost (RFC 3339, retained) |
| phev/wifi/state | The car's WiFi interface (`wifi_interface`): *connected* or *disconnected* (retained) |
| phev/wifi/signal | Its signal level in dBm, while connected (retained) |
| phev/battery/level | Drive battery level, in percent |
| phev/charge/charging | Whether the battery is charging: *on* or *off* |
| phev/charge/plug | Charge plug *unplugged* or *connected* |
| phev/charge/remaining | Minutes left, while charging |
| phev/climate/status | Whether the climate control is on |
| phev/climate/mode | Climate mode, if on: *cool*, *heat*, *windscreen* |
| phev/climate/[mode] | Per mode (*cool*, *heat*, *windscreen*): *on* or *off* |
| phev/door/locked | Whether the car is locked: *on* or *off* |
| phev/door/driver | *closed* or *open*; likewise the doors below |
| phev/door/front_passenger | |
| phev/door/rear_left | |
| phev/door/rear_right | |
| phev/door/bonnet | The bonnet (hood) |
| phev/door/boot | The boot (tailgate) |
| phev/lights/parking | Parking lights: *on* or *off* |
| phev/lights/head | Head lights: *on* or *off* |
| phev/lights/hazard | Hazard lights: *on* or *off* |
| phev/lights/interior | Interior lights: *on* or *off* |
| phev/vin | The car's VIN |
| phev/registrations | Number of WiFi clients registered with the car |

The WiFi state comes from the kernel as it changes, and the signal is what the WiFi
adapter measured from the car anyway, so neither polls or sends anything to the car. The
signal is updated with each status update (`update_interval`, 5 minutes by default).

### Command topics

| Topic | Description |
|---|---|
| phev/set/register/[register] | Set register 0x[register] to the value 0x[payload] |
| phev/set/parkinglights | Parking lights *on* or *off* |
| phev/set/headlights | Head lights *on* or *off* |
| phev/set/cancelchargetimer | Cancel the charge timer (any payload) |
| phev/set/climate/[mode] | Climate *cool*, *heat*, *windscreen* or *off* for [payload] minutes (10, 20 or 30) |
| phev/set/climate/state | `reset` clears the *terminated* state |
| phev/connection | The connection to the car: *on*, *off* or *restart* (reconnect) |
| phev/restart | Restart phev2mqtt itself (any payload; retained messages are ignored) |

`mqtt_disable_register_set_command: true` ignores all `phev/set/...` topics.

## Web interface

The optional web interface shows the car's status and the log, has buttons for the
climate, lights, charge timer, reconnecting and restarting, and lets you set up MQTT, the
car's WiFi and the car's name in Home Assistant. It is off unless `web_listen` is set in
the config file:

```
web_listen: ":8080"        # or "127.0.0.1:8080" for local access only
web_password: "something long"   # required unless listening on localhost
```

Log in with any user name and that password. `web_auth: false` turns the login off
altogether; then anyone who can reach `web_listen` controls the car. Some managed browsers
(e.g. Edge with an `AuthSchemes` policy) refuse basic auth and only show "unauthorized".

 * MQTT settings and the name in Home Assistant saved in the web interface go to
   `/var/lib/phev2mqtt/settings.yaml` (with the package) and override the config file.
 * The WiFi settings need NetworkManager; the package ships a polkit rule that lets only
   the `phev2mqtt` service manage WiFi connections.
 * *Restart phev2mqtt* asks for a second click; the page reconnects by itself.
 * The log keeps the last 500 lines. *Errors only* hides everything else, *Clear* empties
   the view.
 * Setting raw registers is not possible from the web interface.

## Home Assistant

phev2mqtt supports [Home Assistant MQTT discovery](https://www.home-assistant.io/integrations/mqtt/#mqtt-discovery)
by default. The car appears as a device once phev2mqtt has connected to it (it needs the
VIN); search for "phev" in your entities.

![Home Assistant screenshot](phev-ha.png)

Besides the car's own entities there are:

 * **Connection**: whether phev2mqtt is connected to the car. It stays available, so it
   shows the car being away instead of turning unavailable.
 * **WiFi**, **WiFi signal**, **Last connected** and **Last disconnected**, under
   diagnostics.
 * **Restart gateway** and **Reconnect to car** buttons.

The car is called *PHEV &lt;VIN&gt;*. `ha_device_name` gives it another name, as does
*Rename* in the web interface without reconnecting; Home Assistant puts it in front of
every entity name. A name given in Home Assistant itself takes precedence.

Disable discovery with `--ha_discovery=false`, or change its prefix with
`--ha_discovery_prefix` (default `homeassistant`).

## Configuration file

The configuration file is YAML with one level; its keys are the command line options of
`phev2mqtt client mqtt` (see `phev2mqtt client mqtt --help`). The package's
`/etc/phev2mqtt/config.yaml` documents them all. For example:

```
mqtt_server: "tcp://192.168.1.10:1883"
mqtt_username: "phev"
mqtt_password: "secret"
update_interval: 5m
ha_device_name: "Outlander"
wifi_interface: wlan0
wifi_restart_time: 30m
wifi_restart_retry_time: 2m
web_listen: ":8080"
web_password: "something long"
```

`web_password`, `web_auth` and `web_settings_file` can only be set in the config file, so
the password never shows up in the process list. `--config=/dev/null` reads no config
file at all.

## Raspberry Pi setup

With the `.deb` package the service, user and config file are already in place: only the
WiFi steps below apply.

### NetworkManager

Add `/etc/NetworkManager/system-connections/WiFi-PHEV.nmconnection` (mode `600`), or use
the example in `/usr/share/doc/phev2mqtt/examples/`:

```
[connection]
id=WiFi-PHEV
type=wifi
auth-retries=10
autoconnect-priority=10
autoconnect-retries=0
# Replace with your wlan interface name
interface-name=wlan0
# Disable unnecessary protocols
lldp=0
llmnr=0
mdns=0
wait-device-timeout=30000

[wifi]
band=bg
mode=infrastructure
ssid=<REMOTExxxxxx SSID of your vehicle WiFi network>
# Optional for faster connection
bssid=<BSSID/MAC of your vehicle WiFi network>
channel=<3 (default) or 8>
# Optional to emulate connection from your phone
cloned-mac-address=<Your phone MAC address>

[wifi-security]
key-mgmt=wpa-psk
psk=<Password>

[ipv4]
# Static address instead of the car's DHCP, and only route the car's subnet over this
# link (never the default route)
method=manual
address1=192.168.8.47/24
ignore-auto-dns=true
never-default=true
route-metric=40
route1=192.168.8.0/24,192.168.8.46

[ipv6]
method=disabled
```

Then `nmcli connection reload`, `nmcli connection up WiFi-PHEV`, and check it with
`nmcli connection`. NetworkManager reconnects by itself when the connection is lost,
even for a long time. The connection quality depends heavily on the distance between the
car and the Pi and on the walls in between; with a short distance the setup is stable and
`wifi_restart_time` is not needed.

### wpa_supplicant and systemd-networkd

Without NetworkManager, add the car to `/etc/wpa_supplicant/wpa_supplicant.conf`:

```
ctrl_interface=DIR=/var/run/wpa_supplicant GROUP=netdev
update_config=1
country=AU

network={
	ssid="REMOTE45bhds"
	scan_ssid=1
	psk="blahblahbla12314"
}
```

To connect with another MAC address than the adapter's own, add
`/etc/systemd/network/00-default.link`:

```
[Match]
# The 'real' (default) MAC address of the Pi's wireless interface.
MACAddress=b8:27:eb:50:c0:52

[Link]
# The MAC address to use to connect to the car.
MACAddress=ee:4d:ec:de:7a:91
NamePolicy=kernel database onboard slot path
# Optionally force the name of the WiFi network interface
Name=wlan0
```

Make sure the car's network does not become the default route (with systemd-networkd,
e.g. `RouteMetric=200` in its `[DHCP]` section).

### Without the package

Copy the `phev2mqtt` binary to `/usr/local/bin`, and add
`/etc/systemd/system/phev2mqtt.service`, with your MQTT broker:

```
[Unit]
Description=phev2mqtt service script
StartLimitIntervalSec=5
After=network-online.target

[Service]
Type=exec
ExecStart=/usr/local/bin/phev2mqtt --config=/dev/null client mqtt --mqtt_server tcp://192.168.0.88:1883 -v=debug
Restart=always
RestartSec=30s
SyslogIdentifier=phev2mqtt

[Install]
WantedBy=multi-user.target
```

Start it with `sudo systemctl enable --now phev2mqtt`. After a reboot, check that `wlan0`
has the right MAC address and the address `192.168.8.47`, and follow the log with
`sudo journalctl -fu phev2mqtt -o cat`.

## Development

Read the [protocol documentation](protocol/README.md) for how the car talks. `go test
-race ./...` runs the tests.

### Sniffing the official app

A packet dump of the official Mitsubishi app helps finding out more. Phone apps such as
*Packet Capture* and *PCAP Remote* can capture the traffic into PCAP files without root
access; *Packet Capture* saves them on the phone, *PCAP Remote* also allows live sniffing.

Decode a PCAP file with `phev2mqtt decode pcap <file>` (build with `-tags pcap`, see
[Build from source](#build-from-source)). `-v info`, `debug` or `trace` sets the detail,
and `--latency` replays the packets with their original timing, which helps pinpointing
app events. `tcp:<host>:<port>` instead of a file connects to that address and decodes
its traffic, for live sniffing.

### Vehicle emulator

`phev2mqtt emulator` emulates a car, to test without one (and without the risk of
putting your car into weird states). The official app always connects to `192.168.8.46`,
so run it on a machine with that address that the phone can reach over WiFi, e.g. a
Raspberry Pi set up as an access point with hostapd, or tunnel the TCP connection to a
development machine.

The app should be able to register with the emulator (it might take a couple of goes).
Settings sent by the app do not change its state, but it is useful for sniffing the app.
With `--mqtt_server`, registers published to `phev/emu/set/register/<register>` are sent
to the connected client. The emulator does not currently complete the handshake with
phev2mqtt's own client.

## Credits

phev2mqtt was written by **Ben Buxton** ([@buxtronix](https://github.com/buxtronix)),
who reverse engineered most of the protocol, and has been improved by the contributors to
[buxtronix/phev2mqtt](https://github.com/buxtronix/phev2mqtt/graphs/contributors):

 * [@zivillian](https://github.com/zivillian), who reverse engineered the XOR security
   algorithm ([#16](https://github.com/buxtronix/phev2mqtt/issues/16))
 * [@altserg](https://github.com/altserg), [@gmta](https://github.com/gmta),
   [@nagisa](https://github.com/nagisa), [@martingjohn](https://github.com/martingjohn),
   [@IceEyz](https://github.com/IceEyz), [@gnyman](https://github.com/gnyman) and
   [@StMond](https://github.com/StMond)

This fork also includes the upstream pull requests of
[@Will-Honor](https://github.com/Will-Honor) (#62, #63, #64, #65),
[@lazytesting](https://github.com/lazytesting) (#55),
[@Grisu930](https://github.com/Grisu930) (#66) and [@altserg](https://github.com/altserg)
(#50).

phev2mqtt was inspired by [phev-remote](https://github.com/phev-remote) by
[@papawattu](https://github.com/papawattu), whose code helped work out the protocol.

The fork phev2mqtt-nxt is maintained by Andre Thomasse
([@athomasse](https://github.com/athomasse)). Contributions and pull requests are
welcome.

## Licence

Licensed under the GNU General Public License, version 3 or (at your option) any later
version; see [LICENSE](LICENSE).

Copyright 2021 Ben Buxton <bbuxton@gmail.com> and the contributors.
