---
icon: material/new-box
---

!!! question "仅 Pasadizo"

### 结构

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

使用列表中最近一次测试成功的第一个出站。

当选中的出站测试或连接失败时，选择列表中下一个可用的出站。

列表中靠前的出站需连续通过 `recover_after` 次测试后才会被重新选中。

在第一轮测试完成之前，选择列表中的第一个出站。

当列表中的出站是一个组（`urltest`、`fallback` 或 `loadbalance`），且其健康状态在本组两次测试之间发生变化时（例如其自身的测试或断路器发现其所有出站均不可用），立即重新选择出站。

### 字段

#### outbounds

==必填==

用于测试的出站标签列表，按优先级排序。

#### url

用于测试的链接。默认使用 `https://www.gstatic.com/generate_204`。

#### interval

测试间隔。 默认使用 `3m`。

#### timeout

!!! question "仅 Pasadizo"

单次 URL 测试的超时时间。默认使用 `15s`。

在此时间内没有响应的测试视为失败。较短的超时可以更快地发现失效的路径：经由数据包被丢弃的 QUIC 代理时，测试否则会等待完整的 `15s`。

#### idle_timeout

空闲超时。默认使用 `30m`。

#### recover_after

靠前的出站替换当前选中的出站之前必须连续通过的测试次数。默认使用 `3`。

当选中的出站失败时，优先选择已通过该次数测试的出站。

#### interrupt_exist_connections

当选定的出站发生更改时，中断现有连接。

仅入站连接受此设置影响，内部连接将始终被中断。
