---
icon: material/new-box
---

!!! question "Since sing-box 1.15.0"

# Metrics

A listener that serves Prometheus metrics and nothing else. Unlike `GET /metrics` of the [Clash API](./clash-api/), it does not expose the API's control endpoints, so it can be opened to a scraper on another host.

### Structure

```json
{
  "listen": "[::]:9000",
  "path": "/metrics",
  "username": "",
  "password": ""
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
| `sing_box_group_selected`                   | gauge   | `group`, `outbound` | `1` for the selected member of an outbound group, `0` for the others |

Traffic is counted on the outbound that finally handles the connection, not on the groups that select it.

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
