---
icon: material/new-box
---

!!! question "Since sing-box 1.15.0"

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

!!! question "Since sing-box 1.15.0"

The timeout of one URL test. `15s` will be used if empty.

A test that has no answer within it fails. A short timeout finds a dead path sooner: through a QUIC proxy whose packets are dropped, a test otherwise waits the full `15s`.

#### idle_timeout

The idle timeout. `30m` will be used if empty.

#### recover_after

The number of consecutive successful tests an outbound earlier in the list must pass before it replaces the selected outbound. `3` will be used if empty.

When the selected outbound fails, an outbound that has passed this many tests is preferred over one that has not.

#### interrupt_exist_connections

Interrupt existing connections when the selected outbound has changed.

Only inbound connections are affected by this setting, internal connections will always be interrupted.
