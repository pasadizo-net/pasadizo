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
  "timeout": "",
  "failure_threshold": 0,
  "failure_window": "",
  "idle_timeout": "",
  "interrupt_exist_connections": false
}
```

Spreads connections over the healthy outbounds in the list. Each TCP connection and each UDP session goes to one outbound, chosen when it is routed.

An outbound is healthy when its last URL test succeeded. A failed connection does not change it, since it may fail for its destination alone, unless `failure_threshold` is set. Until the first test round has finished, all outbounds are healthy. When no outbound is healthy, all outbounds are used. Only outbounds that support the network of the connection are chosen.

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

#### timeout

!!! question "Since sing-box 1.15.0"

The timeout of one URL test. `15s` will be used if empty.

A test that has no answer within it fails. A short timeout finds a dead path sooner: through a QUIC proxy whose packets are dropped, a test otherwise waits the full `15s`.

#### failure_threshold

!!! question "Since sing-box 1.15.0"

The circuit breaker: an outbound whose connections fail for this many different destinations within `failure_window` is unhealthy at once, as if its URL test had failed, until its next successful test. Destinations count by host, so one destination that fails many times counts once and does not make an outbound unhealthy.

A failure is an error when the connection is opened, not one after it is established. Disabled if empty; `3` is a good start.

#### failure_window

!!! question "Since sing-box 1.15.0"

The time window of `failure_threshold`. `10s` will be used if empty. Needs `failure_threshold`.

#### idle_timeout

The idle timeout. `30m` will be used if empty.

#### interrupt_exist_connections

Interrupt existing connections when the set of healthy outbounds has changed.

Only inbound connections are affected by this setting, internal connections will always be interrupted.
