# PHEV Remote protocol (MY18)

A brief description of the protocol used. Determined by analysis and reverse
engineering, together the looking at the [phev-remote](https://github.com/phev-remote)
code.

## Network transport

The client device connects to the car using an SSID with a default format
of *REMOTExxxxxx* where *xxxxxx* is an arbitrary string.

The car assigns the client an IP address over DHCP, usually 192.168.8.47. The
car itself is 192.168.8.46.

The client communicates to the car's address over TCP port 8080. Within the
TCP connection, binary packets are used to exchange data.

## Packet format

Each packet has the following format:

```text
|  1 byte   | 1 byte   | 1 byte | n bytes ... |   1 byte     |
|   [Type]    [Length]    [Ack]       [Data]     [Checksum]  |
```

The fields are:

### Type

One byte.

The type of the packet. Most packet types have a corresponding response type.
See below for known types.

### Length

One byte

The length of the packet, less two. For example a packet that contains two
data bytes has a length field of 4.

### Ack

One byte

The acknowledgement field. Will be 0 for request packets, and 1 for packets
acknowledging a request type.

### Data

Variable length

The data payload for the packet. Depends on the command. The length of this
field is 4 less than the value in the *Length* field.

### Checksum

One byte

The checksum is a basic packet integrity check. To calculate its value, add
up of all the previous bytes in the packet. The least significant octet is
the checksum.

For example, given a packet of `f3040020`, the sum is 0x117, so the checksum
is `0x17`. Thus the entire packet is `f304002017`.

## Security and XOR encoding

Most packets on the wire are encoded with a simple XOR obfuscation algorithm.
Packets from the car will be encoded using a rolling code scheme, and packets
from the client to the car are expected to be encoded similarly.

The algorithm was reverse engineered by @zivillian in
[this issue](https://github.com/buxtronix/phev2mqtt/issues/16) which
contains more detailed notes.

1. Initial security key

Shortly after client connection (and at occasional times during the connection), the
car sends an initialisation packet of type `0x5e` or `0x4e`, depending on the year.
The payload of this packet is used to firstly derive a `security_key` which is a
single byte.

Using this security key, a `key_map` is generated containing 256 distinct byte
values. These byte values are used for the lifetime of the connection or until
another initialisation packet is received.

Two index values are initialised, starting at zero. We'll call these `s_num` and
`r_num` for the send (to car) and receive (from car) directions. The `key_map`
values at the indices represented by `s_num` and `r_num` are the actual encoding
keys, we will call `s_key` and `r_key`.

2. Packet encoding

After the security key map has been established, all further packets to the car
must be "encrypted" with the current `s_key`. Packets received from the car will
be "encrypted" with the current `r_key`, so must be "decrypted" before further decoding.

The "encryption" is done by simply XORing each byte in the packet with the current
corresponding key value, either `key_map[s_num]` or `key_map[r_num]`. Being XOR,
the "decryption" is done the same symmetrical way.

3. s_num and r_num value incrementing

The send and receive keys (i.e `s_key` and `r_key`) only change when certain
packets are sent/received. Otherwise they may be used for multiple packets.

When the car sends a packet of type `0x6f`, the `r_num` is incremented, hence
the new `r_key` will be `key_map[r_num]`. Note that this change is used for
packets *after* the `0x6f` packet.

When the client sends a packet of type `0xf6`, the `s_num` is incremented, hence
the new `s_key` will be `key_map[s_num]`. Note that this change is used for
packets *after* the `0xf6` packet.

`r_num` or `s_num` rollover back to zero if they increment from `0xff`.

## Packet types

Summary:

| Value | Name              | Direction     | Description                          |
|-------|-------------------|---------------|--------------------------------------|
|  0xf3 | Ping Request      | client -> car | Ping/Keepalive request               |
|  0x3f | Ping response     | car -> client | Ping / Keepalive response            |
|  0x6f | Register changed  | car -> client | Notify a register has been updated   |
|  0xf6 | Register update   | client -> car | Client register change ack/set       |
|  0x5e | Security init     | car -> client | Initialise security keys (MY18)      |
|  0xe5 | Security ack      | client -> car | Ack security keys (MY18)             |
|  0x4e | Security init     | car -> client | Initialise security keys (MY14)      |
|  0xe4 | Security ack      | client -> car | Ack security keys (MY14)             |
|  0x2f | Keepalive request | car -> client | Sent by car to check client presence |
|  0xbb | Bad Xor?          | car -> client | Sent when XOR value is incorrect.    |
|  0xcc | Bad Xor?          | car -> client | Sent when XOR value is incorrect.    |

### Ping request (0xf3)

Format:

```text
[f3][04][00][<seq>][00][<cksum>]
```

Seems to be a keepalive sent to the car from the client. The initial XOR seems to be 00
until a 0x5e packet is received from the car. The `<seq>` increments up to 0x63 then
overflows back to 0x0.

### Ping response (0x3f)

Format:

```text
[3f][04][01][<seq>][00][<cksum>]
```

Response to a 0xf3 packet, sent from the car to the client. The `<seq>` matches the
request, though the XOR seems to be chosen by the car and future register update packets
match this XOR until another ping exchange.

### Register changed (0x6f)

Format:

```text
[6f][<len>][<ack>][<register>][<data>][<cksum>]
```

Used to notify the client that a setting register has changed on the car.

If `<ack>` is zero, then indicates a notification of a register value. If `<ack>`
is one, this is a response to a register setting change requested by the client.

The `<register>` is a one-byte value signifying the corresponding register number.

The `<data>` is variable length and dependent on the specific register.

Registers are described below.

### Register update/ack (0xf6)

```text
[f6][<len>][<ack>][<register>][<data>][<cksum>]
```

Used by the client to notify the car of either an ack of a received register update, or setting a new register value.

If `<ack>` is zero, indicates that a register value is to be changed. The `<register>` and `<value>` fields indicate the register and new value.

If `<ack>` is one, is a response to an update (above) received from the car. The `<register>` field is the register value being acked. The `<data>` field contains a single `0x0`  byte.

### Init security (0x5e)

```text
[5e][0c][00][<data>][<cksum>]
```

Sent by the car after around initial ping/keepalive exchanges.

The `<data>` contents are 12 bytes, used to initialise the XOR encryption key map (see above).

This packet is not XOR encrypted.

### Init ack (0xe5)

```text
[e5][04][01][0100][<cksum>]
```

Sent in response to a 0x5e packet. Always seems to be the same.

This packet is not XOR encrypted.

### Bad XOR (0xbb)

```text
[bb][06][01][<unknown>][<exp>]
```

Sent by the car if a received packet is encoded with the incorrect XOR value.

The meaning of the value in the `<unknown>` field is, well, unknown.

The `<exp>` field contains the XOR value which is expected. If the offending
packet is reset using this value for the Xor, then it will likely be accepted.

This can be a workaround given the current unknown algorithm for generating
XOR values.

## Registers

Registers contain the bulk of information on the state of the vehicle.

### Read registers (car to client)

There seem to be two types of register layout (A/B).

| Register | Name                         | Length | Location  | Fields                        | Description                         |
|----------|------------------------------|--------|-----------|-------------------------------|-------------------------------------|
|0x1       | ??                           | 2      | 0         | [1,1]                         |                                     |
|0x2       | Battery warning              | 4      | 2         | [1,1,1,1]                     |                                     |
|0x3       | ??                           | 3      | 6         | [1,1,1]                       |                                     |
|0x4       | Charge timer settings        | 20 / 1 | 10 / ??   | [3,1,3,1,3,1,3,1,3,1] / []    |                                     |
|0x5       | Climate timer settings       | 16 / 1 | 30 / 235  | [1,2,1,2,1,2,1,2,1,2,1] / [1] |                                     |
|0x6       | ??                           | 20 / 1 | 74 / 46   | [1,17,1,1] / [1]              | Similar to 0x15                     |
|0x7       | ??                           | 1      | 237       | [1]                           |                                     |
|0x8       | ??                           | 1      | 234       | [1]                           |                                     |
|0x9       | ??                           | 1      | 49        | [1]                           |                                     |
|0xa       | ??                           | 1      | 50        | [1]                           |                                     |
|0xb       | ??                           | 1      | 238 / 236 | [1]                           |                                     |
|0xc       | ??                           | 1      | 51        | [1]                           |                                     |
|0xd       | ??                           | 1      | 52        | [1]                           |                                     |
|0xe       | ??                           | 1      | 53        | [1]                           |                                     |
|0xf       | ??                           | 1      | 54        | [1]                           |                                     |
|0x10      | AirCon State                 | 3 / 1  | 55        | [1,1,1] / [1]                 |                                     |
|0x11      | ??                           | 1      | 58        | [1]                           |                                     |
|0x12      | TimeSync                     | 7      | 59        | [7]                           |                                     |
|0x13      | ??                           | 1      | 66        | [1]                           |                                     |
|0x14      | ??                           | 7      | 67        | [6,1]                         |                                     |
|0x15      | VIN                          | 20     | 246 / 74  | [19,11] / [1,17,1,1]          |                                     |
|0x16      | ??                           | 8      | 95        | [8]                           | Seems to convey some other settings |
|0x17      | ??                           | 1      | 103       | [1]                           |                                     |
|0x18      | ??                           | 4 / 16 | 276 / 104 | [1,1,2] / [5,3,5,3]           |                                     |
|0x19      | ??                           | 9      | 120       | [1,5,3]                       |                                     |
|0x1a      | ??                           | 5 / 2  | 129       | [1,1,1,1,1] / [1,1]           |                                     |
|0x1b      | ??                           | 1      | 134       | [1]                           |                                     |
|0x1c      | AirCon Mode                  | 1      | 136       | [1]                           |                                     |
|0x1d      | Battery Level / light status | 4      | 137       | [1,1,1,1]                     |                                     |
|0x1e      | Charge plug status           | 2      | 141       | [1,1]                         |                                     |
|0x1f      | Charge State                 | 3      | 143       | [1,2]                         |                                     |
|0x20      | ??                           | 10     | 146       | [2,2,2,2,2]                   |                                     |
|0x21      | ??                           | 1      | 156       | [1]                           |                                     |
|0x22      | ??                           | 6      | 157       | [2,2,2]                       |                                     |
|0x23      | ??                           | 5      | 163       | [1,1,1,1,1]                   | Maybe AC related                    |
|0x24      | Door Lock Status             | 10     | 168       | [1,1,1,1,1,1,1,1,1,1]         |                                     |
|0x25      | ??                           | 3      | 178       | [1,1,1]                       |                                     |
|0x26      | ??                           | 1      | 181       | [1]                           |                                     |
|0x27      | ??                           | 1      | 182       | [1]                           |                                     |
|0x28      | ??                           | 32     | 183       | [33]                          |                                     |
|0x29      | ??                           | 3 / 2  | 216       | [1,1,1] / [1,1]               |                                     |
|0x2a      | ??                           | 1      | 219       | [1]                           |                                     |
|0x2b      | ??                           | 10     | 220       | [10]                          |                                     |
|0x2c      | ??                           | 1      | 233       | [1]                           |                                     |
|0xc0      | ECU Version                  | 13     | 220       | [10,2,1]                      |                                     |

### 0x02 - Battery warning

### 0x0b - Parking light status

### 0x05 - Climate timer

16 bytes.

|Byte(s) | Description |
|--------|-------------|
| 0      | Unknown     |
| 1-3    | Timer 1     |
| 4-6    | Timer 2     |
| 7-9    | Timer 3     |
| 10-12  | Timer 4     |
| 13-15  | Timer 5     |

#### Timer 1..5

__must be converted from big endian to little endian__ to be useful

```text
0xa5f0afe -> 0xfef0a5
```

```text
0xfef0a5 = 1 11 1111 01111 000 0101001 01
           |         |     |   |        |
         Bit23    Bit16 Bit11 Bit8     Bit0
```

| __Bits__ | Description                                 |
|----------|---------------------------------------------|
| 0-1      | Duration: 0=10min 1=20min 2=30min ?3=40min? |
| 2        | Sunday                                      |
| 3        | Monday                                      |
| 4        | Tuesday                                     |
| 5        | Wednesday                                   |
| 6        | Thursday                                    |
| 7        | Friday                                      |
| 8        | Saturday                                    |
| 9-11     | Minute: 0=0 1=10 2=20 3=30 4=40 5=50        |
| 12-16    | Hour: 0=0 1=1 2=2 ... 21=21 22=22 23=23     |
| 17       | on                                          |
| 18       | off                                         |
| 19-23    | Unknown (unused?)                           |

### 0x10 - Aircon status

3 bytes.

| Byte(s) | Description |
|--|--|
|0 | Unknown |
| 1 | AC operating [0=off 1=on] |
| 2 | Unknown |

02b00b = windscreen on/10min

### 0x12 - Car time sync

### 0x15 - Vin / registration state

Vin info and regstration status.

| Byte(s) | Description |
|--|--|
|0 | Unknown |
|1-18 | VIN (ascii) |
|19 | Number of registered clients |

### 0x17 - Charge timer state

### 0x1c - Aircon mode

Single byte.

| Value | Description |
|--|--|
|0 | Unknown |
|1 | Cooling |
|2 | Heating |
|3 | Windscreen |

### 0x1d - Drive battery level / parking light status

```text
10000003
```

| Byte(s) | Description |
|--|--|
|0 | Drive battery level % |
|1 | Unknown |
|2 | Parking light status 0/1 |
|3 | Unknown |

### 0x1e - Charge plug status

0000 - Unplugged
0001 - Plugged in, not charging

### 0x1f - Charging status

| Byte(s) | Description |
|--|--|
|0 | Charge status [0=not charging 1=charging]|
| 1-2 | Charge time remaining |

### 0x23 - Something AC?

AC timer sniff:

| Mode | Start | End | Register |
|---|---|---|---|
| heat | 0 | 10 | 0001000202 |
| windscreen | 0 | 10 |  0101000202  |

### 0x24 - Door / Lock status

```text
Byte 0
|
1000000000
         |
       Byte 9
```

Below shows what is represented by each byte. For door/boot/bonnet,
the value is 1 if open, else 0.

0 - Lock status [1=locked 2=unlocked]

3 - Drivers Door (side depends if LHD/RHD)

4 - Front Passenger Door (side depends if LHD/RHD)

5 - Read Right Door

6 - Rear Left Door

7 - Boot / Trunk

8 - Bonnet / Hood

9 - Headlight state

### 0xc0 - ECU Version string

A string with the software version of the ECU.

## Write registers (client to car)

| Register | Name                       | Description/values     |
|----------|----------------------------|------------------------|
| 0x5      | Sync time                  | See below              |
| 0x6      | Request udpated state      | 0x3                    |
| 0xa      | Set head lights            | 0x1=on 0x2=off         |
| 0xb      | Set parking lights         | 0x1=on 0x2=off         |
| 0xe      | Save settings??            | Sent after 0xf command |
| 0xf      | Update settings            |                        |
| 0x1a     | Set climate timer          | See below              |
| 0x1b     | Set climate state          | See below              |
| 0x17     | Cancel charge timer        |                        |
| 0x19     | Set charge timer schedule  |                        |
| 0x1a     | Set climate timer schedule |                        |

### 0x05 - Sync Time

8 bytes.

| Byte(s) | Description   |
|---------|---------------|
|       0 | Year - 2000   |
|       1 | Month         |
|       2 | Day of month  |
|       3 | Hour          |
|       4 | Minute        |
|       5 | Second        |
|       6 | Day of week   |
|       7 | Device rooted |

### 0x1a - set climate timer

16 bytes.

|Byte(s) | Description |
|--------|-------------|
| 0-2    | Timer 1     |
| 3-5    | Timer 2     |
| 6-8    | Timer 3     |
| 9-11   | Timer 4     |
| 12-14  | Timer 5     |
| 15     | Unknown     |

The Encoding for Timer 1 to 5 is the same as for [reading](#timer-15)

### 0x1b - set climate state

```text
[02[state][duration][start]]
```

* Byte 0 - 02
* Byte 1 - climate state
  * 01 - cooling
  * 02 - heating
  * 03 - windscreen
* Byte 2 - Duration
  * 00 - 10 mins
  * 01 - 20 mins
  * 02 - 30 mins
* Byte 3 - Delay before start
  * 00 - 0 mins / now
  * 01 - 5 mins
  * 02 - 10 mins

## Notes

Things discovered sniffing the app...

```text
AC data sniff.

Windscreen for 10 mins:

INFO[0000] out  [a1] REGISTER SET  (reg 0x1b data 02030000) 
INFO[0000] in   [a1] REGISTER NTFY (reg 0x10 data 02b00b) 
INFO[0000] in   [55] REGISTER NTFY (reg 0x1a data 0001000000) 
INFO[0000] in   [70] REGISTER NTFY (reg 0x1c data 03)   

Heat in 5 mins for 10 mins:

INFO[0000] out  [70] REGISTER SET  (reg 0x1b data 02020001) 
INFO[0000] in   [01] REGISTER NTFY (reg 0x1a data 0000000101) 
INFO[0000] in   [86] REGISTER NTFY (reg 0x1c data 02)   

Heat in 0 mins for 10 mins:

INFO[0000] out  [73] REGISTER SET  (reg 0x1b data 02020000) 
INFO[0000] in   [73] REGISTER NTFY (reg 0x10 data 02b020) 
INFO[0000] in   [22] REGISTER NTFY (reg 0x1a data 0001000000) 
INFO[0000] in   [50] REGISTER NTFY (reg 0x1c data 02)   

Heat in 0 mins for 20 mins
INFO[0000] out  [4e] REGISTER SET  (reg 0x1b data 02020100) 
INFO[0000] in   [1c] REGISTER NTFY (reg 0x10 data 02b034) 
INFO[0000] in   [e7] REGISTER NTFY (reg 0x1a data 0001010000) 
INFO[0000] in   [c6] REGISTER NTFY (reg 0x1c data 02)   

Cool 0 mins for 10 mins
INFO[0170] out  [5f] REGISTER SET  (reg 0x1b data 02010000) 
INFO[0170] in   [5b] REGISTER NTFY (reg 0x10 data 02b139) 
INFO[0171] in   [18] REGISTER NTFY (reg 0x1a data 0001000000) 
INFO[0171] in   [95] REGISTER NTFY (reg 0x1c data 01)  

Cool 10 mins for 10 mins
SETREG 0x1b: -> 02010002

Climate timer:
t1 12:00 cool for 10 mins repeat sun:
set 0x1a: -> 04c00200fe0700fe0700fe0700fe0701
get 0x05: -> 0100fe0700fe0700fe0700fe0700fe07

t2 07:50 cool for 20 mins repeat sunMonTue:
set 0x1a: -> 04c0021d7a0200fe0700fe0700fe0701
get 0x05: -> 0204c00200fe0700fe0700fe0700fe07

disable all:

set 0x1a: 04c0041d7a0400fe0700fe0700fe0701

reset all:

set 0x1a: 00fe0700fe0700fe0700fe0700fe0701

Unplug charger (disabled due timer):

UPDATEREG 0x0c: 01 -> 04
UPDATEREG 0x0d: 01 -> 02
UPDATEREG 0x1e: 0001 -> 0000

Plug in charger (disabled due timer):

UPDATEREG 0x0d: 02 -> 01
UPDATEREG 0x1e: 0000 -> 0001

Plug in charger (within time period):
phev/register/1e 0002
charge starts:
phev/register/1e 0202

unplug:
phev/register/1e 0002
then:
phev/register/1e 0003

plug in:
phev/register/1e 0002
(charges)

unplug from charging:

UPDATEREG 0x1e: 0202 -> 0102
UPDATEREG 0x1e: 0102 -> 0002
UPDATEREG 0x1e: 0002 -> 0003

plug in, charge:

UPDATEREG 0x1e: 0000 -> 0002
UPDATEREG 0x1e: 0002 -> 0202

Charge timer:

disabl timer3: 0x19: 7d38b00183bd00017c70380200ffff0300ffff0302
enable timer3: 0x19: 7d38b00183bd00017c70380100ffff0300ffff0302

settings at 0x4

setting "exterior lights on rmt unlock:

- off -> 0x0f=2b00
- parking -> 0x0f=2b01
- head -> 0x0f=2b02

setting "charge light cutout"

- off  -> 0x0f=0700
- 1min -> 0x0f=0701
- 2min -> 0x0f=0702
- 5min -> 0x0f=0703
- 10min -> 0x0f=0704

setting "headlights on exiting vehicle"

- off -> 0x0f=2a00
- 15s -> 0x0f=2a01
- 30s -> 0x0f=2a02
- 1min-> 0x0f=2a03
- 3min-> 0x0f=2a04

light ones above followed by setting 0x0e->0x0
```
