---
icon: material/new-box
---

!!! question "Pasadizo only"

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
  "first_byte_timeout": "",
  "stall_timeout": "",
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

!!! question "Pasadizo only"

The timeout of one URL test. `15s` will be used if empty.

A test that has no answer within it fails. A short timeout finds a dead path sooner: through a QUIC proxy whose packets are dropped, a test otherwise waits the full `15s`.

#### failure_threshold

!!! question "Pasadizo only"

The circuit breaker: an outbound whose connections fail for this many different destinations within `failure_window` is unhealthy at once, as if its URL test had failed, until its next successful test. Destinations count by host, so one destination that fails many times counts once and does not make an outbound unhealthy.

A failure is an error when the connection is opened, not one after it is established, unless `first_byte_timeout` is set. Disabled if empty; `3` is a good start.

#### failure_window

!!! question "Pasadizo only"

The time window of `failure_threshold`. `10s` will be used if empty. Needs `failure_threshold`.

#### first_byte_timeout

A TCP connection through an outbound that was written to and got no byte back within this time also counts as a failed connection of that outbound for `failure_threshold`. Disabled if empty; `5s` is a good start. Needs `failure_threshold`.

Set it when the outbounds use a protocol that opens a connection without a round trip to its server, such as Hysteria2 or TUIC: on a dead path such an outbound never fails when a connection is opened, so without this the circuit breaker never sees it, and only the next URL test finds it.

The slow connection itself is not closed. The time counts from the first write, so a protocol in which the server speaks first is not affected, and one destination that is slow to answer counts once.

#### stall_timeout

!!! question "Pasadizo only"

An outbound that keeps its own transport connection to its server, which today is a Hysteria2 outbound with its QUIC connection, is unavailable at once when that connection has sent packets for this time and received none, as if its URL test had failed, until its next successful test. Disabled if empty; `5s` is a good start. It must be between `1s` and `20s`: a connection that receives nothing is closed by its own idle timeout, `30s` by default, and a closed connection is not stalled.

QUIC acknowledges every packet that asks for it, and an idle connection sends a keep alive that asks, so on a path that works packets always come back. A single packet without an answer does not count, since some packets are never answered: the connection has to keep sending. The check reads the connection's counters and sends nothing, so it costs no traffic and finds a dead path between two URL tests, also while no connection is being opened. With several connections (`connections` on the Hysteria2 outbound), the outbound is unavailable when all of them have stalled. Outbounds of other types are not affected.

On a line that loses packets, a short time can call a working path dead: a keep alive and its retransmissions can all be lost. The outbound then comes back with its next URL test.

#### idle_timeout

The idle timeout. `30m` will be used if empty.

#### interrupt_exist_connections

Interrupt existing connections when the set of healthy outbounds has changed.

Only inbound connections are affected by this setting, internal connections will always be interrupted.
