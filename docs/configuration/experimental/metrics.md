---
icon: material/new-box
---

!!! question "Pasadizo only"

# Metrics

A listener that serves Prometheus metrics and nothing else. Unlike `GET /metrics` of the [Clash API](./clash-api/), it does not expose the API's control endpoints, so it can be opened to a scraper on another host.

### Structure

```json
{
  "listen": "[::]:9000",
  "path": "/metrics",
  "username": "",
  "password": "",
  "sources": false
}
```

### Fields

#### listen

==Required==

The address to listen on, `host:port`.

#### path

The path of the metrics. `/metrics` will be used if empty.

Every other path answers `404 Not Found`, and every method but `GET` and `HEAD` answers `405 Method Not Allowed`.

#### username

HTTP basic authentication user name. Requests without the matching user name and password answer `401 Unauthorized`.

No authentication if empty: keep the listener on a trusted address or behind a firewall then.

#### password

HTTP basic authentication password. Needs `username`.

#### sources

Also serve the traffic and the open connections of each source address, per inbound: the numbers of the Clash API's `GET /stats/sources?group=inbound`.

Each client address that ever connected adds three series, so keep it off on a server that anyone can reach, and turn it on where the clients are known, such as a home gateway. The Clash API's `GET /metrics` never writes them.

### Metrics

The response is in the Prometheus text format, version 0.0.4. The Clash API's `GET /metrics` returns the same metrics.

| Metric                                      | Type    | Labels              | Description                                                          |
|---------------------------------------------|---------|---------------------|----------------------------------------------------------------------|
| `sing_box_connections`                      | gauge   |                     | Number of open connections                                           |
| `sing_box_upload_bytes_total`               | counter |                     | Bytes sent through all outbounds                                     |
| `sing_box_download_bytes_total`             | counter |                     | Bytes received through all outbounds                                 |
| `sing_box_memory_inuse_bytes`               | gauge   |                     | Memory in use by the Go runtime                                      |
| `sing_box_outbound_connections`             | gauge   | `outbound`          | Number of open connections per outbound                              |
| `sing_box_outbound_upload_bytes_total`      | counter | `outbound`          | Bytes sent per outbound                                              |
| `sing_box_outbound_download_bytes_total`    | counter | `outbound`          | Bytes received per outbound                                          |
| `sing_box_outbound_delay_milliseconds`      | gauge   | `outbound`          | Delay of the last successful URL test                                |
| `sing_box_outbound_delay_timestamp_seconds` | gauge   | `outbound`          | Unix time of the last successful URL test                            |
| `sing_box_outbound_transport_connections`   | gauge   | `outbound`          | Open transport connections (QUIC) of the outbound to its server. This and the next four exist for outbounds that keep such connections, today Hysteria2 |
| `sing_box_outbound_transport_rtt_milliseconds` | gauge | `outbound`         | Smoothed round trip time of those connections, the highest of them; 0 without a connection |
| `sing_box_outbound_transport_packets_sent_total` | counter | `outbound`     | Packets sent on the open connections. Starts again at zero with a new connection |
| `sing_box_outbound_transport_packets_received_total` | counter | `outbound` | Packets received on the open connections                             |
| `sing_box_outbound_transport_packets_lost_total` | counter | `outbound`     | Packets declared lost on the open connections. `rate()` of it over `rate()` of the sent packets is the loss of the path to the server |
| `sing_box_group_selected`                   | gauge   | `group`, `outbound` | `1` for the selected member of an outbound group, `0` for the others |
| `sing_box_inbound_connections_total`        | counter | `inbound`, `network` | Connections accepted per inbound; `network` is `tcp` or `udp`       |
| `sing_box_inbound_errors_total`             | counter | `inbound`           | Inbound connections that failed to route or to dial their outbound   |
| `sing_box_outbound_dials_total`             | counter | `outbound`          | Dials through the outbound                                           |
| `sing_box_outbound_dial_errors_total`       | counter | `outbound`          | Failed dials through the outbound                                    |
| `sing_box_outbound_dial_duration_seconds`   | histogram | `outbound`        | Duration of dials through the outbound, failed ones included         |
| `sing_box_source_connections`               | gauge   | `source`, `inbound` | Number of open connections per source address; only with `sources`  |
| `sing_box_source_upload_bytes_total`        | counter | `source`, `inbound` | Bytes sent per source address; only with `sources`                   |
| `sing_box_source_download_bytes_total`      | counter | `source`, `inbound` | Bytes received per source address; only with `sources`               |
| `process_start_time_seconds`                | gauge   |                     | Start time of the process, in Unix seconds                           |

Traffic is counted on the outbound that finally handles the connection, not on the groups that select it.

A dial is counted on every outbound that opens one: the outbound that handles a connection, the `detour` of an outbound, and the member that a group dials when it is itself used as a detour. A connection through an outbound with a detour therefore counts one dial on each. URL tests and DNS queries through a detour are dials too.

The dial duration buckets are `0.05`, `0.1`, `0.25`, `0.5`, `1`, `2.5`, `5`, `10` and `30` seconds.

`sing_box_inbound_errors_total` does not count connections that a client closed or canceled, nor connections that a `reject` rule refused.

Counters start when the process starts and only increase; `process_start_time_seconds` tells a restart apart from a reset.

### Example

```json
{
  "experimental": {
    "metrics": {
      "listen": "[::]:9000"
    }
  }
}
```

Prometheus scrape configuration:

```yaml
scrape_configs:
  - job_name: pasadizo
    static_configs:
      - targets: ["proxy.example.com:9000"]
```

Example alerting rules:

```yaml
groups:
  - name: pasadizo
    rules:
      # More than 20% of the connections of an inbound fail.
      - alert: PasadizoInboundErrors
        expr: |
          sum by (inbound) (rate(sing_box_inbound_errors_total[5m]))
            / sum by (inbound) (rate(sing_box_inbound_connections_total[5m])) > 0.2
        for: 10m
      # An outbound keeps failing to dial.
      - alert: PasadizoOutboundDialErrors
        expr: rate(sing_box_outbound_dial_errors_total[5m]) > 0.1
        for: 10m
      # p99 dial latency of an outbound above 5s.
      - alert: PasadizoOutboundSlowDials
        expr: histogram_quantile(0.99, sum by (outbound, le) (rate(sing_box_outbound_dial_duration_seconds_bucket[5m]))) > 5
        for: 10m
      - alert: PasadizoRestarted
        expr: changes(process_start_time_seconds[15m]) > 0
```
