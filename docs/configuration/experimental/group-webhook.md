!!! question "Since sing-box 1.15.0"

Group webhook sends an HTTP `POST` request when an outbound group changes its selected outbound,
or when a member of a [URLTest](/configuration/outbound/urltest/), [Fallback](/configuration/outbound/fallback/)
or [LoadBalance](/configuration/outbound/loadbalance/) group changes its availability.

A LoadBalance group has no single selected outbound, so it sends only `health` events.

Events are queued and delivered in order in the background, so a slow or unreachable endpoint
never blocks routing.

### Structure

```json
{
  "url": "http://127.0.0.1:9000/hook",
  "headers": {},
  "http_client": "", // or {}
  "timeout": "",
  "max_attempts": 0,
  "queue_size": 0
}
```

### Fields

#### url

==Required==

The HTTP or HTTPS URL that receives the events.

#### headers

HTTP headers to send with each request, for example `Authorization`.

#### http_client

HTTP Client for sending the events.

See [HTTP Client Fields](/configuration/shared/http-client/) for details.

When empty, the default HTTP client is used: the one named by
[`default_http_client`](/configuration/route/#default_http_client), or the first top-level
`http_clients` entry when `default_http_client` is empty.

#### timeout

Timeout of each request.

`5s` is used by default.

#### max_attempts

Maximum number of attempts to deliver one event, including the first one.

A request is retried after a connection error, a timeout, or a `408`, `429` or `5xx` response.
The delay between attempts starts at `1s` and doubles up to `30s`.

`3` is used by default.

#### queue_size

Maximum number of events waiting for delivery. New events are dropped when the queue is full.

`64` is used by default.

### Event

The request body is a JSON object:

```json
{
  "type": "selected",
  "group": "auto",
  "network": "",
  "outbound": "",
  "from": "hy2-a",
  "to": "hy2-b",
  "delay": 0,
  "reason": "url_test",
  "time": "2026-11-27T10:00:00.123456789Z"
}
```

`network`, `outbound` and `delay` are omitted when empty.

#### type

| Type       | Description                                                                                                 |
|------------|-------------------------------------------------------------------------------------------------------------|
| `selected` | The group selected another outbound. `from` and `to` are the outbound tags.                                 |
| `health`   | A member of a URLTest, Fallback or LoadBalance group changed its availability. `outbound` is the member, `from` and `to` are `available` or `unavailable`, and `delay` is the test delay in milliseconds when it became available. |

The first selection after startup is not reported. In a URLTest, Fallback or LoadBalance group, members are
considered available until the first test of the group completes, so a member that fails that test is reported as `unavailable`.

#### network

`tcp` or `udp` when a URLTest or Fallback group selected a different outbound for only one network. Empty when the
change applies to both.

#### reason

| Reason           | Description                                                                                                  |
|------------------|--------------------------------------------------------------------------------------------------------------|
| `select`         | The outbound of a Selector group was changed through the API.                                                |
| `url_test`       | The group finished a test.                                                                                   |
| `history_update` | The test history of a member changed outside a test of the group, for example after a connection through it failed. |
