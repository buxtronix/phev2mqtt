# Vehicle settings registers.

Send new settings to register 0x0f.

Current settings are sent by the vehicle in register
0x16. It repeats this register a number of times,
each containing a different setting(s). Specific bit
changes in settings are known for many settings,
however it's not yet known what the full structure
of this register is. Each setting is bookended by
bytes `0x00` and `0x02`.

Below are some of the settings as shown in the app,
together with the register to send to set the value,
and the line in 0x16 that is affected by the setting.
The bookend bytes are not included (so only 6 bytes are shown).

# Exterior lights / interior lights

## Auto light sensitivity 

| Setting | Set 0x0f to | Value in 0x16 register |
| ------- | ----------- | ---------------------- |
| Early   | 0900        | 3e091ec83e47           |
| Somewhat early | 0901 | 3e491ec83e47           |
| Normal  | 0902        | 3e891ec83e47           |
| Somewhat late | 0903  | 3ec91ec83e47           |
| Late    | 0904        | 3f091ec83e47           |

## Headlight auto cutout

| Setting | Set 0x0f to | Value in 0x16 register |
| ------- | ----------- | ---------------------- |
| Off     | 0c00        | 420cfe8b01ca           |
| On      | 0c05        | 424cfe8b01ca           |

## Headlights on when exiting vehicle

| Setting | Set 0x0f to | Value in 0x16 register |
| ------- | ----------- | ---------------------- |
| Off     | 2a00        |  3e2a01e901e8          |
| 15s     | 2a01        |  3e6a01e901e8          |
| 30s     | 2a02        |  3eaa01e901e8          |
| 1m      | 2a03        |  3eea01e901e8          |
| 3m      | 2a04        |  3f2a01e901e8          |

## Exterior lights on with remote unlock

| Setting | Set 0x0f to | Value in 0x16 register |
| ------- | ----------- | ---------------------- |
| Off     |  2b00       | 002d002c0e2b           |
| Parking |  2b01       | 002d002c0e6b           |
| Headlights | 2b02     | 002d002c0eab           |

## Interior light auto cutout time

| Setting | Set 0x0f to | Value in 0x16 register |
| ------- | ----------- | ---------------------- |
| Off     | 0d00        | 064f060e1e0d           |
| 3m      | 0d01        | 064f060e1e4d           |
| 30m     | 0d02        | 064f060e1e8d           |
| 60m     | 0d03        | 064f060e1ecd           |

## Duration dome light remains on after door closed

| Setting | Set 0x0f to | Value in 0x16 register |
| ------- | ----------- | ---------------------- |
| 0s      | 0b00        | 434cfe0b01ca           |
| 7.5s    | 0b01        | 434cfe4b01ca           |
| 15s     | 0b02        | 434cfe8b01ca           |
| 30s     | 0b03        | 434cfecb01ca           |
| 1m      | 0b04        | 434cff0b01ca           |


## Charge lid light auto cut-out

| Setting | Set 0x0f to | Value in 0x16 register |
| ------- | ----------- | ---------------------- |
| Off     | 0700        | 3e091ec83e07           |
| 1m      | 0701        | 3e091ec83e47           |
| 3m      | 0702        | 3e091ec83e87           |
| 5m      | 0703        | 3e091ec83ec7           |
| 10m     | 0704        | 3e091ec83f07           |

## Reset vehicle settings (fe00)


# Wipers

## Windshield wipers intermittent operation

| Setting | Set 0x0f to | Value in 0x16 register |
| ------- | ----------- | ---------------------- |
| Fixed   |                 0400 | 1e860e851e0402 |
| Adjustable |              0401 | 1e860e851e4402 |
| Adjustable/Speedsensitive | 0402 | 1e860e851e8402 |
| Adjustable/Rainsensitive  | 0403 | 1e860e851ec402 |

## Wipers linked to washer

| Setting | Set 0x0f to | Value in 0x16 register |
| ------- | ----------- | ---------------------- |
| Off     |      0500   | 1e860e051ec402         |
| On      |      0501   | 1e860e451ec402         |
| On/Finish wipe | 0502 | 1e860e851ec402         |

## Comfort washer

| Setting | Set 0x0f to | Value in 0x16 register |
| ------- | ----------- | ---------------------- |
| Off     | 0f00        | 60f060e1e4d02          |
| On      | 0f01        | 64f060e1e4d02          |

## Rear wiper int interval

| Setting | Set 0x0f to | Value in 0x16 register |
| ------- | ----------- | ---------------------- |
| 0s      | 0600        | 1e060e851ec402         |
| 4s      | 0601        | 1e460e851ec402         |
| 8s      | 0602        | 1e860e851ec402         |
| 16s     | 0603        | 1ec60e851ec402         |

Rear wiper in reverse

| Setting | Set 0x0f to | Value in 0x16 register |
| ------- | ----------- | ---------------------- |
| When rear on       | 1d00 |  65e061d01dc02     |
| When front/rear on | 1d01 |  65e065d01dc02     |


