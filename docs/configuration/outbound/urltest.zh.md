!!! quote "sing-box 1.15.0 中的更改"

    :material-plus: [timeout](#timeout)

### 结构

```json
{
  "type": "urltest",
  "tag": "auto",
  
  "outbounds": [
    "proxy-a",
    "proxy-b",
    "proxy-c"
  ],
  "url": "",
  "interval": "",
  "timeout": "",
  "tolerance": 50,
  "idle_timeout": "",
  "interrupt_exist_connections": false
}
```

### 字段

#### outbounds

==必填==

用于测试的出站标签列表。

#### url

用于测试的链接。默认使用 `https://www.gstatic.com/generate_204`。

#### interval

测试间隔。 默认使用 `3m`。

#### tolerance

以毫秒为单位的测试容差。 默认使用 `50`。

#### timeout

!!! question "自 sing-box 1.15.0 起"

单次 URL 测试的超时时间。默认使用 `15s`。

在此时间内没有响应的测试视为失败。较短的超时可以更快地发现失效的路径：经由数据包被丢弃的 QUIC 代理时，测试否则会等待完整的 `15s`。

#### idle_timeout

空闲超时。默认使用 `30m`。

#### interrupt_exist_connections

当选定的出站发生更改时，中断现有连接。

仅入站连接受此设置影响，内部连接将始终被中断。