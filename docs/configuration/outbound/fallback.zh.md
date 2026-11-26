---
icon: material/new-box
---

!!! question "自 sing-box 1.15.0 起"

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
  "idle_timeout": "",
  "recover_after": 3,
  "interrupt_exist_connections": false
}
```

使用列表中最近一次测试成功的第一个出站。

当选中的出站测试或连接失败时，选择列表中下一个可用的出站。

列表中靠前的出站需连续通过 `recover_after` 次测试后才会被重新选中。

### 字段

#### outbounds

==必填==

用于测试的出站标签列表，按优先级排序。

#### url

用于测试的链接。默认使用 `https://www.gstatic.com/generate_204`。

#### interval

测试间隔。 默认使用 `3m`。

#### idle_timeout

空闲超时。默认使用 `30m`。

#### recover_after

靠前的出站替换当前选中的出站之前必须连续通过的测试次数。默认使用 `3`。

当选中的出站失败时，优先选择已通过该次数测试的出站。

#### interrupt_exist_connections

当选定的出站发生更改时，中断现有连接。

仅入站连接受此设置影响，内部连接将始终被中断。
