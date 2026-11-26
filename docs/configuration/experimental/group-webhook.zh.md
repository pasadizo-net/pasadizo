!!! question "自 sing-box 1.15.0 起"

当出站组更换所选出站，或 [URLTest](/zh/configuration/outbound/urltest/)、[Fallback](/zh/configuration/outbound/fallback/) 或 [LoadBalance](/configuration/outbound/loadbalance/) 组的成员可用性发生变化时，
组 Webhook 会发送 HTTP `POST` 请求。

事件在后台排队并按顺序发送，因此缓慢或不可达的端点不会阻塞路由。

LoadBalance 组没有单一的所选出站，因此只发送 `health` 事件。

### 结构

```json
{
  "url": "http://127.0.0.1:9000/hook",
  "headers": {},
  "http_client": "", // 或 {}
  "timeout": "",
  "max_attempts": 0,
  "queue_size": 0
}
```

### 字段

#### url

==必填==

接收事件的 HTTP 或 HTTPS URL。

#### headers

每个请求附带的 HTTP 头，例如 `Authorization`。

#### http_client

用于发送事件的 HTTP 客户端。

参阅 [HTTP 客户端字段](/zh/configuration/shared/http-client/) 了解详情。

留空时使用默认 HTTP 客户端：即由 [`default_http_client`](/zh/configuration/route/#default_http_client)
指定的客户端，或当 `default_http_client` 为空时使用顶级 `http_clients` 的第一项。

#### timeout

每个请求的超时时间。

默认使用 `5s`。

#### max_attempts

发送单个事件的最大尝试次数，包括首次尝试。

在连接错误、超时或 `408`、`429`、`5xx` 响应后重试。重试间隔从 `1s` 开始，每次翻倍，最多 `30s`。

默认使用 `3`。

#### queue_size

等待发送的最大事件数。队列已满时丢弃新事件。

默认使用 `64`。

### 事件

请求体为 JSON 对象：

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

`network`、`outbound` 和 `delay` 为空时省略。

#### type

| 类型         | 描述                                                                                                |
|------------|---------------------------------------------------------------------------------------------------|
| `selected` | 组选择了另一个出站。`from` 和 `to` 为出站标签。                                                                    |
| `health`   | URLTest、Fallback 或 LoadBalance 组的成员可用性发生变化。`outbound` 为该成员，`from` 和 `to` 为 `available` 或 `unavailable`，变为可用时 `delay` 为测试延迟（毫秒）。 |

启动后的首次选择不会上报。在 URLTest、Fallback 或 LoadBalance 组中，成员在组的首次测试完成前视为可用，因此首次测试失败的成员会上报为 `unavailable`。

#### network

当 URLTest 或 Fallback 组仅为一种网络选择了不同的出站时为 `tcp` 或 `udp`。变化同时适用于两种网络时为空。

#### reason

| 原因               | 描述                                         |
|------------------|--------------------------------------------|
| `select`         | 通过 API 更改了 Selector 组的出站。                  |
| `url_test`       | 组完成了一次测试。                               |
| `history_update` | 成员的测试历史在组测试之外发生变化，例如通过该成员的连接失败后。 |
