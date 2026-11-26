---
icon: material/new-box
---

!!! question "Since sing-box 1.15.0"

### Structure

```json
{
  "type": "loadbalance",
  "tag": "balance",

  "outbounds": [
    "proxy-a",
    "proxy-b",
    "proxy-c"
  ],
  "strategy": "",
  "url": "",
  "interval": "",
  "idle_timeout": "",
  "interrupt_exist_connections": false
}
```

Spreads connections over the healthy outbounds in the list. Each TCP connection and each UDP session goes to one outbound, chosen when it is routed.

An outbound is healthy when its last URL test succeeded; a failed connection does not change it. Until the first test round has finished, all outbounds are healthy. When no outbound is healthy, all outbounds are used. Only outbounds that support the network of the connection are chosen.

### Fields

#### outbounds

==Required==

List of outbound tags to balance over.

#### strategy

The balancing strategy, `consistent-hashing` will be used if empty.

| Strategy             | Description                                                                                                                                                                                                                         |
|----------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `consistent-hashing` | Choose the outbound by a hash of the destination: the domain if the destination has one, else the IP address. The same destination always uses the same outbound, and when an outbound becomes unhealthy, only its destinations move. |
| `round-robin`        | Choose the healthy outbounds in turn.                                                                                                                                                                                               |

#### url

The URL to test. `https://www.gstatic.com/generate_204` will be used if empty.

#### interval

The test interval. `3m` will be used if empty.

#### idle_timeout

The idle timeout. `30m` will be used if empty.

#### interrupt_exist_connections

Interrupt existing connections when the set of healthy outbounds has changed.

Only inbound connections are affected by this setting, internal connections will always be interrupted.
