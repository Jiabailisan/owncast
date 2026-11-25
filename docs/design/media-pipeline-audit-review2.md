# Owncast 媒体链路与播放器二轮对抗复审修复记录

- **基线范围**: `f799bb305b1e3f74e6145e825aa26269c87e035b..f51deb0b6f8c79098e546ced4cf9032c2a4cf977`
- **触发时机**: 四批整改推送完毕后的全分支二轮审查（CGO 全量测试 + `-race` + 前后端独立对抗审查）

## 修复清单

### 后端 Go（6 项 + 1 项夹具修正）

1. **`services/stream/webrtc.go` (`configureWebRTC`) [High]**:
   - 原先仅判断 `s.webRTC == nil`，未判断 `!s.webRTCDeliveryEnabled()`。因为 `main.go` 在 WebRTC 禁用时仍会注入非 nil 的 `WebRTCProvider`，导致禁开 WebRTC 时依然进入 `needsMeasuredBitrate` 并触发长达 2.5s / 32 MiB 的 `awaitSourceBitrate` 预读，且随后调用 `SetVariants` / `openWebRTCLegs` 产生误报的 `ErrDisabled` 告警。
   - **修复**: 入口改为 `if !s.webRTCDeliveryEnabled()`，立即清空 `webRTCLegs` 与 `webRTCWarnings` 并返回 `webRTCOutputs{}`。
2. **`services/stream/webrtc.go` (`configureWebRTC` `SetVariants` 失败路径) [Medium]**:
   - `SetVariants` 失败回退为 HLS-only 前，已记录的 `webRTCLegsStatus` 仍保持 `copy`/`transcode`，会误导 `/api/config` 与管理端实况状态。
   - **修复**: 在 `SetVariants` 返回错误时将全部 `legs` 标记为 `disabled` 并清空 `OutputBitrateKbps`，再调用 `s.setWebRTCLegs(legs)`。
3. **`services/stream/stream.go` (`applyStreamOffline`) [Medium]**:
   - `runningProcessModel` 仅在开播时写入，从未在下播时清空，导致离线状态下修改进程模型时 `/api/admin/live/status` 与 `/api/admin/live/ffmpeg` 仍显示已结束直播的旧进程模型。
   - **修复**: 在 `applyStreamOffline` 的 `defer` 清理阶段加锁清空 `s.runningProcessModel = ""`。
4. **`webserver/handlers/config.go` (`webRTCVariantResponse.IsPassthrough`) [High]**:
   - `json:"isPassthrough,omitempty"` 会在 `IsPassthrough == false` 时省略字段，破坏前端 `chooseEncodedWebRTCVariant` 对非直通编码档位的识别。
   - **修复**: 移除 `,omitempty`，改为 `json:"isPassthrough"`，并在 `webserver/handlers/config_batch4_test.go` 增加 JSON 序列化断言。
5. **`services/ingest/flv/inspector.go` (`rateTracker.add`) [Low]**:
   - 若畸形 FLV tag 产生接近 `2^31` ms 的极端前向跳变，有符号 `span` 会回绕为负数导致旧样本驱逐循环提前 `break`。
   - **修复**: 改为 `if span >= 0 && span <= bitrateWindowMs { break }`。
6. **`services/webrtc/whep_disabled_delete_test.go` [Minor]**:
   - 移除无用的 `models` 导入及 `_ = models.WebRTCConfig{}` 占位语句。
7. **`services/stream/ingest_tee_align_test.go` (`buildFLVStream`) [测试夹具修复]**:
   - 原测试辅助函数在写入 `PreviousTagSize` 之后才更新 `previous = 11 + size`，使每个 tag 尾部写成了上一个 tag 的尺寸；启用 CGO 跑通 `services/stream` 测试后被 Batch 4 严格的 FLV `PreviousTagSize == 11 + dataSize` 校验暴露。
   - **修复**: 按 FLV 规范在每个 tag 后写入当前 tag 的 `11 + size`。

### 前端 Web（6 项）

8. **`web/components/video/metrics/playback.js` [Critical]**:
   - `@videojs/http-streaming` 默认的 `videojs.Vhs.xhr` 分发器带有 `.original === true`，其内部逻辑为 `videojs.Vhs.xhr.original === true ? videojs.xhr : videojs.Vhs.xhr`。直接将其作为 `baseVjsXhr` 委托会在首个 `.m3u8`/`.ts` 请求触发无限递归栈溢出。
   - **修复**: 保留 `this.previousVhsXhr = videojs?.Vhs?.xhr` 供 `stop()` 原样恢复，但构造 `baseVjsXhr` 时当 `this.previousVhsXhr.original === true` 时绕过分发器直接委托给 `videojs?.xhr`；同时在 `stop()` 中对已销毁的 `player` 做 `isDisposed?.()` 守卫。在 `web/tests/playbackMetrics.test.ts` 新增真实 VHS 分发器递归回归测试。
9. **`web/components/video/webrtc/webrtc-stats.ts` 与 `WebRTCPlayer.tsx` [High]**:
   - 对后端可能省略 `isPassthrough: false` 的兼容性做防御处理：存在结构化布尔标记时将 `variant.isPassthrough !== true` 视为非直通档位。
10. **`web/components/video/webrtc/WebRTCPlayer.tsx` [High]**:
    - 在连接建立后同步执行的 `evaluateConnectionState()` 与 `track.readyState === 'ended'` 检查之后立即补齐 `if (cancelled || disconnectHandled) return;`，防止同步触发 `handleRuntimeDisconnect` 后继续穿透执行 `setPhase('ready')`、触发 `onReady` 或启动泄露的 stats beacon。
11. **`web/components/video/OwncastPlayer/OwncastPlayer.tsx` [Medium]**:
    - 修复 HLS VHS 统计中增量字节与累计时长混用导致的码率/网速失真：将码率对 `(windowBitrateBytes, windowBitrateSeconds)` 与下载吞吐对 `(windowTransferBytes, windowTransferMs)` 原子成对更新，处理计数器回退重置，并以 `parseOptionalFinite` 避免 `Number(null) === 0` 误判。
12. **`web/components/video/OwncastPlayer/OwncastPlayer.tsx` [Medium]**:
    - 修复 `online: true -> false` 切换及 `getVideoQualities()` 异步未决期间销毁导致的 `LatencyCompensator` / `PlaybackMetrics` 定时器泄露：在 `player.on('dispose', ...)` 统一释放并清空 `playerRef`，且在 `await getVideoQualities()` 返回后校验 `disposed || player.isDisposed?.() || playerRef.current !== player`。
13. **`web/components/video/latencyCompensator.js` [Low]**:
    - `setQualityPinned(true)` 入口新增 `if (this.pinnedQualitySnapshot) { return; }` 幂等守卫，避免重复锁定清空已保存的档位快照。
14. **`web/components/video/PlayerContainer.tsx` [Hygiene]**:
    - 补齐 `useEffect` 依赖数组中的 `streamIdentity`，消除 `react-hooks/exhaustive-deps` 告警。
