# Packet corrupt 排查：缺口最早出现在 SRT 接收端

现场三场 SRT 推流（d714000bc9）的 `Packet corrupt (stream = 0)` 事件的分层定位结论。

## 1. 两条同 DTS 日志的归属

现场 10:49 场（`srt-1IWqkFRdg`，Isolated）的 corrupt 事件出现**两组独立上下文指针**：

- `0x5579b853e4c0`/`...e180`：一个 ffmpeg 进程
- `0x557658143880`/`...e640`：另一个 ffmpeg 进程

对照 transcoder.log 的启动顺序与命令行（nobuffer/analyzeduration/RTP 输出 = WebRTC 进程；
var_stream_map/-f hls = HLS 进程），确认**两路 FFmpeg（HLS 与 WebRTC）各自报了 corrupt**。
10:47 场（Shared，单进程）也出现同一模式的双指针，但那是一个进程内的 demuxer 与
内部上下文，不是两个进程。

## 2. 决定性证据：两路 DTS 完全一致

10:49 场两路进程报的 corrupt DTS **逐一相同**：

| DTS | WebRTC (e4c0/e180) | HLS (880/640) |
|---|---|---|
| 2074920 | Y | Y |
| 3961920 | Y | Y |
| 3963420 | Y | Y |
| 3967920 | Y | Y |
| 3969420 | Y | Y |

tee 只做字节扇出、不改内容——两路看到相同的损坏字节，说明缺口在分流之前。

## 3. 分层定位（本地复现）

用受控丢包（TS 层每 N 包丢一包 → SRT 传输）在 d714000bc9 上复现：

- SRT 接收端统计：`packetLossPct=3.9%`，`droppedPackets=28`
- corrupt 事件 8 次，两个 ffmpeg 上下文同样报出**相同 DTS**（205500、337500）
- 对 Isolated 两路 ffmpeg stdin 的抓包做 TS 连续计数检查：
  - 先注册的分支（WebRTC）：视频 PID 256 有 **8 个连续计数缺口**（1325024、1409624、…）
  - 后注册的分支（HLS）：其抓包窗口内 0 缺口——因为它加入较晚，且 tsAligner
    在撕裂包后重新同步，跳过了缺口区域

结论：**缺口最早出现在 SRT 接收端**（网络丢包未被重传完全恢复），进入 owncast
字节流的 TS 已经带缺口，分流只是把它原样送到了两路 FFmpeg。这不是 owncast
tee 或分支注册引入的问题。

## 4. 现场三场与丢包的对应

| 场 | 模型 | SRT loss | corrupt 事件 |
|---|---|---|---|
| 10:46:25 srt-SgmlkFRdR | isolated | 11.56% | 0（17.6 Mbps 短场） |
| 10:47:23 srt-5zuXzFgOR | shared | 1.41% | 57 次，DTS 268920 起 bursts |
| 10:48:37 srt-1IWqkFRdg | isolated | 19.03% | 22 次，两路 DTS 一致 |

corrupt 的爆发与 SRT 丢包率正相关；Shared 场（单进程）也出现 corrupt，进一步
排除 Isolated 分流为成因。

## 5. 复测脚本

`.test-runs/goal-pt/capture/run-loss-repro.sh`（含 TS 丢包注入器
`ts_dropper.py`）可复跑：注入率、双路 stdin 抓包、live-status 的 SRT 接收统计、
transcoder.log 的 corrupt 事件均可对照。

## 6. 未完成

- 现场 10:49 的**逐时刻** SRT 接收端 drop/late/retransmit 曲线未采集（日志只有
  session 结束时的聚合值）；下一次现场推流建议同时轮询 `/api/admin/live/status`
  的 `ingest.droppedPackets`，以便与 corrupt DTS 逐条对齐。
- 发送端封装后的 TS 未抓取：需要在 OBS 侧录制 UDP 输出做同样的连续计数检查，
  以确认发送端本身无缺口（本地复现已表明接收端丢包足以产生缺口，但未直接
  排除发送端）。
