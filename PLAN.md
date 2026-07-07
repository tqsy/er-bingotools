# BingoTools 直播流改造计划

## 目标

将 `bingotools.html` 的直播播放方式从「iframe 嵌入 B站播放器 / 抖音截屏采集」改造为「通过自实现取流逻辑拿到真实直播流地址，经同源反向代理在 `<video>` 中直接播放」，最终交付**单 exe**（Wails v2 + Go + 内嵌前端）。

逻辑参考自有 fork：`/home/soar/src/yt-dlp` 的 `douyin-live-support` 分支（`DouyinLiveIE`）与 master 的 `BiliLiveIE`。

## 背景结论

- **B站直播**：`BiliLiveIE` 取流无签名、无 cookie，纯 JSON API，重写工作量低（~80 行 Go）。
- **抖音直播**：`DouyinLiveIE` 需 `a_bogus` 签名（SM3 + RC4 + 自定义 base64 表 + 字节拼装）+ `ttwid` cookie，是重写核心难点（~350 行 Go）。
- **CORS 根源**：直播流 CDN 不返回 CORS 头，浏览器无法跨域直接拉流；FLV/HLS 还需自定义 `Referer`，浏览器 `<video>` 无法设置请求头。必须由原生层做同源反向代理注入头。
- **方案选型**：Wails v2 (Go)。`AssetServer.Handler` 提供同源 `/` 路径代理，天然无 CORS、无跨平台 Origin 差异、无强制 CSP；Go 的 `uint32`/`0xFFFFFFFF` 语义与 Python 接近，a_bogus 字节拼装可 1:1 移植；`net/http` + `io.Copy` 流式转发极简；`crypto/rc4` 标准库自带。

## 环境前置（已就绪）

- Go 1.26.4、Rust 1.96.1、Node 22.23.1、Python 3.12.13、ffmpeg 8.1.2 均已安装。
- yt-dlp fork 在 `/home/soar/src/yt-dlp`，`douyin-live-support` 分支已确认可取流。

---

## 阶段总览

| 阶段 | 内容 | 风险 | 状态 |
|------|------|------|------|
| P0 | a_bogus 签名 Go 重写 + Python 对照验证 | 高 | ✅ 完成 |
| P1 | B站取流 Go 重写 | 低 | ✅ 完成 |
| P2 | 抖音取流 Go 重写（依赖 P0） | 中 | ✅ 完成 |
| P3 | Wails 骨架 + 同源反向代理 | 中 | ✅ 完成 |
| P4 | 前端播放器改造（flv.js / hls.js） | 低 | ✅ 完成 |
| P5 | 抖音 + 裁切 + 本地采集适配 | 低 | ✅ 完成 |
| P6 | 打包单 exe + 验收 | 低 | ✅ 完成 |

> 当前进度：P0–P6 全部完成。已产出 Windows / Linux 双平台可执行文件。

---

## P0 — a_bogus 签名 Go 重写与验证（最高优先）✅ 已完成

**状态**：已通过。Go 重写与 Python 参照字节级一致，并在真实抖音直播间验证能取到可播放的流。

### 步骤

1. **从 fork 抽取签名代码**：定位 `yt_dlp/extractor/tiktok.py` 中以下函数，作为移植蓝本：
   - `_douyin_rc4_encrypt`
   - `_douyin_left_rotate` / `_douyin_get_t_j` / `_douyin_ff_j` / `_douyin_gg_j`
   - `_DouyinSM3`（`reset`/`write`/`_fill`/`_compress`/`sum`）
   - `_douyin_result_encrypt` / `_douyin_get_long_int` / `_douyin_gener_random` / `_douyin_generate_random_str`
   - `_douyin_generate_rc4_bb_str` / `_douyin_ab_sign`

2. **新建独立验证目录** `bingotools/absign/`，用 Go 1:1 移植上述函数。命名保持对应（如 `douyinAbSign(query, userAgent string) string`）。

3. **构造 Python 黄金参照**：在 fork 目录用 Python 调用真实函数，对若干组 `(query, userAgent)` 输入产出 `a_bogus` 输出，写入 `absign/testdata/golden.json`。

4. **Go 单测对照**：`absign/ab_sign_test.go` 读 golden.json，断言 Go 输出与 Python 完全一致。覆盖：
   - 不同 query 长度（短/长/含中文/含 `&` `=`）
   - 不同 userAgent
   - 边界：空 query

5. **验证标准**：全部 case 字节级一致 → P0 通过，解锁后续。

### 风险与对策

- Python 整数与 Go 不同：Go 需用 `uint32` 并在移位/相加处 `& 0xFFFFFFFF` 防溢出；`int(num/256/256/...)` 的地板除在 Go 用 `>>`。
- Python `chr()` 对 >255 的值会越界，Go 用 `byte` 截断需与 Python 行为对齐（确认 Python 实际输入范围）。
- 若无法完全一致：定位到具体子函数（SM3 优先，因其是哈希，输出固定），逐步二分。

### 完成情况

- `bingotools/absign/ab_sign.go`（移植实现，~360 行）：RC4、SM3、resultEncrypt、generateRandomStr、generateRc4BbStr、AbSign。
- `bingotools/absign/douyin_sign_ref.py`：从 fork 精确提取的 Python 参照（保持原样，不可手改）。
- `bingotools/absign/gen_golden.py`：固定时间戳生成黄金参照，含各层中间产物。
- `bingotools/absign/testdata/golden.json`：黄金值。
- `bingotools/absign/ab_sign_test.go`：8 个测试全过（RC4、SM3+标准向量、resultEncrypt、generateRandomStr、中间产物、generateRc4BbStr、abSign）。
- `bingotools/absign/cmd/e2e`：端到端验证，对真实在播抖音直播间取到 HLS 流地址，ffmpeg 探测可播放（1920x1080 H.264/AAC）。

**关键发现**：手抄编码表 s3 时曾误用 s4 的内容，靠 golden 字节级对照捕获修正——印证了「先验证再继续」的必要性。重写时务必用程序从源码精确提取常量，避免手抄。

---

## P1 — B站取流 Go 重写 ✅ 已完成

**状态**：已通过。对真实在播直播间（房间 6）取到 8 个格式，ffmpeg 经代理验证可播放（H.264 1280x720 30fps + AAC）。

### 取流逻辑（来自 `BiliLiveIE`）

```
1. GET https://api.live.bilibili.com/room/v1/Room/get_info?id=<roomId>
   → data.live_status（1 = 直播中）
2. GET https://api.live.bilibili.com/xlive/web-room/v2/index/getRoomPlayInfo
     ?room_id=<roomId>&qn=<qn>&codec=0,1&format=0,2&mask=0
     &no_playurl=0&platform=web&protocol=0,1
   遍历 _FORMATS qn：80/150/250/400/10000/20000/30000
   → playurl_info.playurl.stream[].format[].codec[]
     · 过滤 codec.current_qn == qn
     · url_info[] → host + base_url + extra 拼成流 URL
3. 播放头：Referer = https://live.bilibili.com/<roomId>
```

### 步骤

1. 新建 `bingotools/internal/bilibili/bilibili.go`。
2. 实现 `Resolve(roomId string) (*LiveStream, error)`：
   - `getInfo`：校验 `live_status`。
   - `getRoomPlayInfo`：遍历 `_FORMATS` 的 qn 列表，解析 formats。
   - 选最优格式（按 qn 降序，优先 HLS，FLV 兜底）。
   - 返回 `{Kind: "hls"|"flv", URL, Referer, UA}`。
3. `bilibili_test.go`：对一个公开直播间号做真实请求，打印 formats，人工确认 URL 可在 ffmpeg 中播放（`ffmpeg -headers "Referer:..." -i <url> -t 5 -f null -`）。

### 完成情况

- `internal/bilibili/bilibili.go`：`Client.Resolve` 实现 get_info + getRoomPlayInfo 双 API 取流，`PickPreferred` 选优（优先 http_hls/fmp4）。真实返回验证：protocol 为 `http_hls`，ext 为 `fmp4`（实为 HLS m3u8）。
- `internal/bilibili/bilibili_test.go`：真实取流测试通过。

---

## P3 — Wails 骨架 + 同源反向代理 ✅ 已完成

**状态**：已通过。同源反向代理打通 B站与抖音两端，ffmpeg 经代理 URL 成功播放。

### 步骤

1. `wails init -n bingotools -t vanilla`（纯 HTML 模板，契合单文件前端）。
2. 把 `bingotools.html` 放入 `frontend/dist/index.html`，`main.go` 用 `//go:embed all:frontend/dist` 嵌入。
3. 定义 App 结构与缓存：
   ```go
   type streamCache struct {
       URL, Referer, Cookie, UA string
   }
   type App struct {
       ctx      context.Context
       streams  sync.Map // side(1|2) -> *streamCache
   }
   ```
4. 实现 Bound 方法 `ResolveLive(side int, source string) (string, error)`：
   - `parseLiveSource`（移植自前端逻辑）：`bilibili:<roomId>` / `douyin:<web_rid|url>`。
   - 调用对应模块取流，写入 `streams` 缓存，返回 `kind`（"hls"/"flv"）。
5. 实现 `AssetServer.Handler` 同源反向代理：
   - 路由 `/live/stream/{side}`：取缓存 → `http.NewRequest` 注入 `Referer`/`Cookie`/`User-Agent` → `io.Copy(w, resp.Body)` 流式转发。
   - 透传 `Range` 请求头与 `206` 状态，支持 seek。
   - 设 `Access-Control-Allow-Origin: *` 作保险（同源本不需要）。
6. `main.go` 配置：
   ```go
   AssetServer: &assetserver.Options{
       Assets:  assets,
       Handler: http.HandlerFunc(app.proxyHandler),
   },
   OnStartup: app.startup,
   Bind: []interface{}{app},
   ```

### 验证

- 启动应用，B站直播间设好后，浏览器 DevTools 看 `/live/stream/1` 返回 200，`<video>` 正常播放。
- 抖音未接入前先用 B站验证代理链路通。

### 完成情况

- Wails v2.13.0 集成，`main.go` 用 `embed.FS` 嵌入 `frontend/dist/index.html`。
- `internal/proxy/proxy.go`：同源反向代理。核心特性：
  - 路由 `/live/stream/<side>[/<segment>]`，支持主 m3u8 与分片转发。
  - 嗅探首块内容修正 Content-Type（B站 m3u8 常返回 text/plain，修正为 `application/vnd.apple.mpegurl`）。
  - 注入 Referer/Cookie/UA，透传 Range（seek）。
- `internal/app/app.go`：`ResolveLive(side, source)` Bound 方法，解析 `bilibili:<id>` / `douyin:<id>` 并缓存到代理。
- `cmd/headless`：无窗口端到端验证器，ffmpeg 经代理 URL 播放成功。
- 代理同时验证了 P2 抖音取流（提前完成）。

---

## P2 — 抖音取流 Go 重写（依赖 P0）✅ 已完成

**状态**：已通过。复用 P0 的 absign，对真实在播抖音直播间取到 4 个 HLS 格式，ffmpeg 经代理验证可播放（H.264 1920x1080 25fps + AAC）。

### 取流逻辑（来自 `DouyinLiveIE`）

```
1. web_rid = live.douyin.com/<id>
2. GET 该页 → 读取 Set-Cookie: ttwid=...（若未持有）
3. query = {aid:6383, app_name:douyin_web, live_id:1, device_platform:web,
            language:zh-CN, browser_language:zh-CN, browser_platform:Win32,
            browser_name:Chrome, browser_version:116.0.0.0,
            web_rid:<web_rid>, is_need_double_stream:false, msToken:""}
4. a_bogus = _douyin_ab_sign(原始query, UA)
5. GET https://live.douyin.com/webcast/room/web/enter/?<query>&a_bogus=<sig>
     Header: Referer https://live.douyin.com/, UA; Cookie: ttwid
6. 解析：
   stream_url.stream_orientation == 2 → pull_datas[].stream_data.data.{quality}.main.{flv,hls}
   否则 → live_core_sdk_data.pull_data.stream_data.data.{quality}.main.{flv,hls}
   兜底 → hls_pull_url_map / flv_pull_url_map
   优先 HLS，FLV 兜底
```

### 步骤

1. 新建 `bingotools/douyin/douyin.go`。
2. 实现 `Resolve(webRid string) (*LiveStream, error)`：
   - ttwid 获取与缓存（cookie jar）。
   - 组 query → 调 P0 的 `douyinAbSign` → 请求 enter API。
   - 解析 `stream_orientation` 分支与兜底 map。
3. `douyin_test.go`：对真实直播间号取流，ffmpeg 验证可播放。

### 完成情况

- `internal/douyin/douyin.go`：`Client.Resolve` 实现取 ttwid cookie → a_bogus 签名 → enter API → 解析 stream_url。支持 `stream_orientation==2` 的 pull_datas 分支与 live_core_sdk_data 分支，兜底 hls_pull_url_map/flv_pull_url_map。
- `internal/douyin/douyin_test.go`：parseStreamURL 与 extractWebRid 单测通过。

---

## P4 — 前端播放器改造 ✅ 已完成

**状态**：已通过。`bingotools.html` 中的 iframe 已替换为 `<video>`，通过 hls.js/flv.js 直接播放同源代理流，JS 语法检查与调用链测试通过。

### 改造内容

1. **HTML**：
   - `iframe#frame1/2` → `video.live-video#liveVideo1/2`。
   - 裁切预览 `iframe#cropEditorFrame` → `video#cropEditorVideo`。
   - 在 `</body>` 前引入 `frontend/dist/hls.min.js` 与 `flv.min.js`（离线内嵌，无 CDN 依赖）。
2. **CSS**：
   - `.live-iframe` 规则迁移到 `.live-video`，并加 `object-fit: contain; background: #000;`。
   - `.live-crop-zoom` 同时适配 `.live-video` 与 `.live-local-video`。
3. **JS**：
   - 新增全局工具函数 `isGoBound()`、`stopNetworkStream(side)`、`loadStream(side, source)`。
   - `loadStream` 流程：
     - 清理本地采集 video，显示网络流 video。
     - 调用 `window.go.app.App.ResolveLive(side, source.type + ':' + source.value)`。
     - 按返回 `kind` 创建播放器：
       - `flv`：用 `flvjs.createPlayer({type:'flv', url:'/live/stream/'+side, isLive:true})`。
       - `hls`：优先 `new Hls().loadSource('/live/stream/'+side).attachMedia(video)`；Safari 原生回退。
     - 切换前 `destroy()` 旧播放器，避免内存泄漏与画面残留。
   - `confirmLiveId()`：不再打开 B站嵌入页或抖音浏览器页，统一调用 `loadStream(currentLiveNum, source)`。
   - `restoreLiveFramesFromSaved()`：启动时自动对保存来源调用 `loadStream`。

### 验证

- `node --check` 提取后的主脚本语法通过。
- JSDOM 集成测试：调用 `loadStream(1, {type:'bilibili', value:'6'})` → 正确调用 `go.app.App.ResolveLive(1, 'bilibili:6')` → `#liveVideo1` 取消隐藏。
- 人工代码审查确认：B站/抖音设置后均走 `loadStream`；旧 iframe 引用已清除；播放器实例在切换前被销毁。

### 产出

- 改造后的 `bingotools.html` 与 `frontend/dist/index.html`（保持一致）。
- `frontend/dist/hls.min.js`、`frontend/dist/flv.min.js`。
- `scripts/patch_p4.py`：记录本次前端改造的自动化补丁脚本。

---

## P5 — 适配裁切 / 本地采集 / 边界 ✅ 已完成

**状态**：已完成。P4 改造过程中已同步处理；`applyLiveCropToMain` 同时作用于网络流 video 与本地采集 video，本地采集时自动停止网络流播放器。

### 改造内容

1. **裁切功能**：
   - `applyLiveCropToMain(side)` 改为操作 `#liveVideo${side}` 与 `#localVideo${side}`。
   - zoom 容器 `.live-crop-zoom` 的 transform 对内部两种 video 都生效。
2. **裁切预览**：
   - `openLiveCropFromSet()`：本地采集活跃时复用 `srcObject`；网络流时用 `video.src = '/live/stream/' + side` 预览。
   - `resetCropEditorPreview()` 清理 video 的 src/srcObject。
3. **本地采集**：
   - `captureLocalScreen()` 在获取屏幕流前调用 `stopNetworkStream(side)`，关闭旧网络播放器。
   - `stopLocalScreen()` 清理本地 video，并将 `liveVideo` 重新显示（若该侧无网络流则显示提示）。
4. **放大布局与提示**：`mode-zoom-left/right` 的 grid 定位无需改动；取流失败时通过 `setLiveTip` 与 `showToast` 给出提示。

### 产出

- 改造后的 `bingotools.html`/`frontend/dist/index.html` 中裁切与本地采集逻辑已同步迁移。

---

## P6 — 打包单 exe + 验收 ✅ 已完成

**状态**：已完成。Windows `.exe` 与 Linux 可执行文件均已构建成功；后端取流经 headless 验证可播放。

### 构建配置

新增 `wails.json`：
```json
{
  "$schema": "https://wails.io/schemas/config.v2.json",
  "name": "bingotools",
  "outputfilename": "bingotools.exe",
  "frontend:build": "echo ok",
  "frontend:dev": "echo ok",
  "frontend:install": "echo ok",
  "wailsjsdir": "./frontend",
  "version": "2.13.0"
}
```

### 构建命令

- **Windows**（在 WSL/Linux 交叉编译）：
  ```bash
  wails build -platform windows/amd64
  ```
- **Linux**（本机，使用 webkit2gtk-4.1）：
  ```bash
  wails build -tags webkit2_41 -platform linux/amd64
  ```

### 产物

| 文件 | 大小 | 说明 |
|------|------|------|
| `bingotools-windows-amd64.exe` | 12 MB | Windows 单文件可执行程序 |
| `bingotools-linux-amd64` | 11 MB | Linux 可执行程序（验证用） |

### 验证结果

| 验收项 | 状态 | 备注 |
|--------|------|------|
| B站直播间取流 → 代理可播放 | ✅ | `go run ./cmd/headless bilibili 6`：ffmpeg 探测到 H.264 1280x720 30fps + AAC |
| 抖音直播间取流 → 代理可播放 | ✅ | `go run ./cmd/headless douyin 208823316033`：ffmpeg 探测到 H.264 852x480 25fps + AAC |
| 前端资源嵌入 | ✅ | `strings` 确认 `index.html`、`hls.min.js`、`flv.min.js`、`liveVideo1`、`ResolveLive` 均已嵌入 |
| Linux 版可启动 | ✅ | `./bingotools-linux-amd64` 成功初始化 WebKit 并进入事件循环 |
| Windows 版单 exe | ✅ | PE32+ GUI x86-64，由 Wails CLI 产出 |
| B站/抖音设置后 `<video>` 直接播放 | ⚠️ | 代码与调用链已验证；需真实 Windows GUI 环境做最终画面确认 |
| 左右双直播间 / 裁切 / 本地采集 / 放大布局 | ⚠️ | 代码已迁移；需真实 GUI 环境做最终交互确认 |

### 关键修复

P6 测试中发现 B站 M3U8 使用相对分片路径，而前端请求代理 URL 不带尾斜杠，导致播放器/ffmpeg 把分片解析到错误路径（`/live/stream/xxx.m4s`）。已在 `internal/proxy/proxy.go` 中增加 **M3U8 相对路径重写**：代理返回主 playlist 时，把所有相对 `URI` 与分片行重写为 `/live/stream/<side>/<segment>`，并新增单元测试覆盖。

### 运行依赖

- **Windows**：需要系统已安装 **WebView2 Runtime**（Windows 11 自带；Windows 10 部分旧版需单独安装）。
- **Linux**：需要 `libwebkit2gtk-4.1-0` 与 `libgtk-3-0`（运行时，非开发包）。

### 产出

- `bingotools-windows-amd64.exe`
- `bingotools-linux-amd64`
- `wails.json`

---

## 后续修复记录

### 修复：抖音 `www.douyin.com/follow/live/<rid>` URL 解析错误

**问题**：输入 `https://www.douyin.com/follow/live/577242340198` 时，`extractWebRid` 错误地提取了 path 第一段 `follow`，导致请求 `live.douyin.com/follow`，返回 "room_data 获取失败"。

**根因**：旧实现取 `TrimPrefix(path, "/")` 后按第一个 `/` 截断，只适配 `live.douyin.com/<rid>` 这种单段 path。

**修复**：
- `internal/douyin/douyin.go` 的 `extractWebRid` 改为：
  1. 优先从 query 取 `room_id` / `from_room_id`。
  2. 再取 path 的 basename（最后一段）。
  3. 校验 rid 为数字/字母/下划线组合，非法时 fallback 到 query。
- `internal/douyin/douyin_test.go` 增加 `www.douyin.com/follow/live/<rid>`、`room_id` query、`from_room_id` query 等用例。

**验证**：`go run ./cmd/headless douyin 'https://www.douyin.com/follow/live/577242340198'` 成功解析并 ffmpeg 探测到 H.264 1280×720 60fps + AAC。

---

## 风险登记

| 风险 | 影响 | 缓解 |
|------|------|------|
| a_bogus Go 重写与 Python 不一致 | 抖音取流失败 | P0 优先单独验证，字节级对照 |
| 抖音前端更新致 a_bogus 失效 | 抖音取流失败 | 持续维护；模块化便于更新 |
| B站流需 Referer | `<video>` 无法直接播放 | 代理层注入 Referer |
| FLV vs HLS 兼容性 | 部分格式无法播放 | 优先 HLS，FLV 兜底；两种播放库都集成 |
| WebView2 依赖 | Win10 旧机无法运行 | 随附 bootstrapper 静默安装 |
| 直播流地址过期 | 长时间播放中断 | `<video>` 错误时自动重新 ResolveLive |

## 依赖与参考

- 取流逻辑蓝本：`/home/soar/src/yt-dlp/yt_dlp/extractor/tiktok.py`（`DouyinLiveIE`，`douyin-live-support` 分支）
- 取流逻辑蓝本：`/home/soar/src/yt-dlp/yt_dlp/extractor/bilibili.py`（`BiliLiveIE`，master）
- Wails 文档：`AssetServer.Handler`（同源 `http.Handler` 反向代理）
- 目标前端：`/home/soar/src/bingotools/bingotools.html`（5639 行，iframe 嵌入 + 截屏采集）
