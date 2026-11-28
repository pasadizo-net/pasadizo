---
icon: material/new-box
---

!!! question "Pasadizo only"

### Structure

```json
{
  "type": "fallback",
  "tag": "fallback",
  
  "outbounds": [
    "proxy-a",
    "proxy-b",
    "proxy-c"
  ],
  "url": "",
  "interval": "",
  "timeout": "",
  "idle_timeout": "",
  "recover_after": 3,
  "lazy": [],
  "stall_timeout": "",
  "interrupt_exist_connections": false
}
```

Uses the first outbound in the list whose last test succeeded.

When the selected outbound fails a test or a connection, the next available outbound in the list is selected.

An outbound earlier in the list is selected again only after it has passed `recover_after` consecutive tests.

Until the first test round has finished, the first outbound in the list is selected.

When an outbound in the list is a group (`urltest`, `fallback` or `loadbalance`) and its health changes between the tests of this group, for example when its own tests or its circuit breaker find all its outbounds unavailable, the outbound is selected again at once.

### Fields

#### outbounds

==Required==

List of outbound tags to test, in order of preference.

#### url

The URL to test. `https://www.gstatic.com/generate_204` will be used if empty.

#### interval

The test interval. `3m` will be used if empty.

#### timeout

!!! question "Pasadizo only"

The timeout of one URL test. `15s` will be used if empty.

A test that has no answer within it fails. A short timeout finds a dead path sooner: through a QUIC proxy whose packets are dropped, a test otherwise waits the full `15s`.

#### idle_timeout

The idle timeout. `30m` will be used if empty.

#### recover_after

The number of consecutive successful tests an outbound earlier in the list must pass before it replaces the selected outbound. `3` will be used if empty.

When the selected outbound fails, an outbound that has passed this many tests is preferred over one that has not.

#### lazy

Outbounds of the list that are tested only while they are needed: when no outbound before them is available, or while one of them is the selected outbound. The first outbound cannot be lazy. Availability counts per network: when no outbound before a lazy one carries UDP, the lazy one is needed for UDP and is tested all the time.

Use it for a backup on a metered or slow link, such as mobile data or a satellite: while the outbounds before it work, it is never tested, so it costs no traffic, and it is never selected. When the last outbound before it fails a test, it is tested in the same round; when that outbound fails a connection between two rounds, a round starts at once.

The health of a lazy outbound that is not being tested is not reported to the [group webhook](/configuration/experimental/group-webhook/).

#### stall_timeout

!!! question "Pasadizo only"

An outbound that keeps its own transport connection to its server, which today is a Hysteria2 outbound with its QUIC connection, is unavailable at once when that connection has sent packets for this time and received none, as if its URL test had failed, until its next successful test. Disabled if empty; `5s` is a good start. It must be between `1s` and `20s`: a connection that receives nothing is closed by its own idle timeout, `30s` by default, and a closed connection is not stalled.

QUIC acknowledges every packet that asks for it, and an idle connection sends a keep alive that asks, so on a path that works packets always come back. A single packet without an answer does not count, since some packets are never answered: the connection has to keep sending. The check reads the connection's counters and sends nothing, so it costs no traffic and finds a dead path between two URL tests, also while no connection is being opened. With several connections (`connections` on the Hysteria2 outbound), the outbound is unavailable when all of them have stalled. Outbounds of other types are not affected.

On a line that loses packets, a short time can call a working path dead: a keep alive and its retransmissions can all be lost. The outbound then comes back with its next URL test.

#### interrupt_exist_connections

Interrupt existing connections when the selected outbound has changed.

Only inbound connections are affected by this setting, internal connections will always be interrupted.
