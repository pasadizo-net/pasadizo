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
  "idle_timeout": "",
  "recover_after": 3,
  "interrupt_exist_connections": false
}
```

Uses the first outbound in the list whose last test succeeded.

When the selected outbound fails a test or a connection, the next available outbound in the list is selected.

An outbound earlier in the list is selected again only after it has passed `recover_after` consecutive tests.

### Fields

#### outbounds

==Required==

List of outbound tags to test, in order of preference.

#### url

The URL to test. `https://www.gstatic.com/generate_204` will be used if empty.

#### interval

The test interval. `3m` will be used if empty.

#### idle_timeout

The idle timeout. `30m` will be used if empty.

#### recover_after

The number of consecutive successful tests an outbound earlier in the list must pass before it replaces the selected outbound. `3` will be used if empty.

When the selected outbound fails, an outbound that has passed this many tests is preferred over one that has not.

#### interrupt_exist_connections

Interrupt existing connections when the selected outbound has changed.

Only inbound connections are affected by this setting, internal connections will always be interrupted.
