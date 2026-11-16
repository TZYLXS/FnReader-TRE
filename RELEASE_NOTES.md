# FnReader-TRE 0.2

**2026-11-17 · 适配 FnReader 服务端 2.6.17 / 2.6.18**

OpenWrt / LEDE / ARM 软总线解析桥，纯静态 Go 二进制，LuCI 管理，解析缓存放 USB。

## 本版变更

### 媒体类任务（2.6.17+）

- 新增 `cover` / `intro` / `bookinfo` 三类任务：抓取 `bookUrl` 详情页，读取
  `og:*` / `meta` / JSON-LD / 封面 `img` / 简介区块，不依赖书源规则。
- `cover` 回 `{coverUrl}`；`intro` 回 `{intro, author, kind, wordCount, lastChapter,
  totalChapterNum}`；`bookinfo` 合并简介与封面。
- 取不到时回 `ok:false`（`fetch` / `not-found`），由服务端回退 `bookinfo` 或本机抓取。
- 默认领取类型扩展为 `ebook-parse,ebook-chapter,cover,intro,bookinfo`；
  升级脚本会把 0.1 的旧默认自动补齐，自定义配置不覆盖。

### 软总线路由与幂等（2.6.17/18）

- 识别服务端隐藏性能分路由：低分设备只领 `media`，高分设备领 `book`/`content`，
  在线无高分设备时全部放开；引擎不伪造分数。
- `pending` 解析 `taskClass` 并写入日志。
- result 响应兼容 `ok:"duplicate"` / `"stale"` / `ignored` 与 `accepted:false`，
  均按终态处理，不重试。
- 媒体任务超时 45 秒；整本 12 分钟、单章 3 分钟不变。

## 产物

| 文件 | 大小 | SHA256 |
| --- | --- | --- |
| `fnparse_0.2_armv7.ipk` | 2.8 MB | `14d8421deaaf6767d2d8ba05ba80a861cdd94943ac2e7448b50debe4d147892e` |
| `fnparse_0.2_armv7.apk` | 2.8 MB | `f14dba0de644fb2fa188b03a0f97e7df446c434e930133e36e8e69af547886e6` |
| `fnparse_0.2_arm64.ipk` | 2.6 MB | `2da2e895c0477a20a414459e3f74cc19a83187ac624071ab8072638534593dac` |
| `fnparse_0.2_arm64.apk` | 2.6 MB | `1a7328d29cac48a6c2f856c91f791d60a4612996bdfb0cd806a2d2ec00df1bba` |

安装：

```sh
opkg install --force-architecture /tmp/fnparse_0.2_armv7.ipk
# 或 OpenWrt apk 分支
apk add --allow-untrusted /tmp/fnparse_0.2_armv7.apk
```

## 校验

- `go test ./...` 通过；`go vet ./...` 无告警；
- 四包版本 0.2、ELF 架构 ARM/AArch64、默认领取类型校验通过
  （`python scripts/verify_pkg.py`）。

## 已知限制

- 媒体任务只解析静态 HTML；JS 动态渲染或需登录的详情页可能取不到，失败由服务端兜底。
- 不实现 `search` / `toc` / `content` / `rank`（需要书源规则引擎）。
- 新设备隐藏分为 50（低分），已有高分手机在线时只会领到 `media` 类；
  没有高分设备在线时全部类型放开，仍可领 `ebook-*`。
