# FnReader-TRE

**FnReader 软总线解析引擎（OpenWrt / LEDE / ARM）**

版本：**0.3（书源规则解析 + 只承接简介/封面，适配 FnReader 服务端 2.6.17/18）**

发布说明见 [`RELEASE_NOTES.md`](RELEASE_NOTES.md)。

把 FnReader 软总线里的「简介 / 封面」任务落到路由器上：设备作为软总线节点，写死单线程、
每次只领 1 个任务，按服务端书源 `ruleBookInfo` 规则解析书籍详情页（规则不命中时用
og/meta、JSON-LD 等通用网页解析兜底），把 `cover` / `intro` / `bookinfo` 结果回传。
附 LuCI 管理界面，安装包内自带全部运行环境（纯静态 Go 二进制，零第三方依赖）。

---

## 1. 它解决什么问题

FnReader（legado-web）默认由服务端或手机 App 解析电子书。TRE 把这件事交给常年在线、
功耗低的路由器：

- 承接 2.6.17+ 的 `cover` / `intro` / `bookinfo` 媒体类任务，只读详情页；
- 详情页优先按服务端书源 `ruleBookInfo` 规则解析（`/api/m/sources.json` 本地缓存 10 分钟），
  规则不命中时回退通用网页解析，取不到才回 `ok:false`；
- 领取策略写死：单线程、每次 1 个任务、只领媒体类；服务端误派 `book`/`content` 时
  立即回传 `unsupported`（服务端扣分并兜底），等效隐藏分封顶 50；
- 解析缓存与书源缓存放在 USB，不写路由器 flash；
- 服务端离线/任务过期时仍由服务端本机兜底，路由器掉线不影响阅读。

## 2. 兼容性

| 平台 | 包格式 | 架构 | 说明 |
| --- | --- | --- | --- |
| OpenWrt 23.05 及更早 / LEDE（opkg） | `.ipk` | `arm_cortex-a7_neon-vfpv4` / `aarch64_cortex-a53` | 主力支持 |
| OpenWrt 24.10（opkg） | `.ipk` | 同上 | 24.10 仍是 opkg，直接用 ipk |
| OpenWrt 25.12+ / apk 分支 | `.apk` | 同上 | 未签名包，`apk add --allow-untrusted` |

> 说明：OpenWrt 24.10 官方仍然使用 opkg；apk 包格式从 25.12 / main 分支开始。两种包都提供，
> 装错格式时换另一个即可。其他 armv7 命名（`arm_cortex-a9_neon` 等）可用
> `opkg install --force-architecture` 或 `apk add --arch <设备arch>`。

## 3. 安装

把 `dist/` 里对应架构的包传到路由器 `/tmp`：

```sh
# opkg（OpenWrt 23.05 / 24.10 / LEDE）
opkg install --force-architecture /tmp/fnparse_0.3_armv7.ipk

# apk（OpenWrt 25.12+ / apk 分支）
apk add --allow-untrusted /tmp/fnparse_0.3_armv7.apk
```

安装后：

1. 浏览器打开 LuCI → **服务 → FnReader 解析引擎**；
2. 填「接入 IP / 服务端地址」（如 `http://192.168.5.110:1122`）和 Token；
3. 插 U 盘，点「刷新状态」确认检测到 USB；缓存目录留空即自动选盘；
4. 保存后点「重启引擎」，状态变绿即上线；
5. 服务端「设备」页应看到 `owrt-xxxxxxxxxx` 节点在线。

从 0.2 升级时，安装脚本会把 `claim_types` 强制改为 `cover,intro,bookinfo`
（本插件定位已写死为「只承接简介/封面」），并删除废弃的 `concurrency` 项。
历史版本（0.1 / 0.2）的包归档在 `dist/old/`。

开机自启由 procd 管理：

```sh
/etc/init.d/fnparse enable
/etc/init.d/fnparse start
logread -f | grep fnparse
```

## 4. LuCI 设置项

| 选项 | 默认 | 说明 |
| --- | --- | --- |
| 启用引擎 | 开 | 关闭后不启动服务 |
| 接入 IP / 服务端地址 | `http://192.168.5.110:1122` | legado-web 地址，可省略 `http://` |
| Token | 空 | 服务端访问令牌，用于 pending/result/上传 |
| 设备名 | 空 | 留空自动 `OpenWRT-<hostname>` |
| 解析缓存目录 | 空 | 建议 `/mnt/sda1/fnparse`；留空自动找 USB |
| 自动检测 USB | 开 | 扫描 `/mnt/*` 可写挂载点 |
| 长轮询等待（秒） | 20 | 服务端上限 30 |
| 分片上限（MB） | 2 | 50 章或该字节数切分，不拆章 |
| 缓存上限（MB） | 2048 | 超出按最旧清理 |
| 原文件保留（小时） | 48 | 过期随清理删除 |
| 日志级别 | info | debug/info/warn/error |
| 领取任务类型（写死） | `cover,intro,bookinfo` | 插件只承接简介/封面；单线程、每次领 1 个 |

> 0.2 的「并发解析数」已废弃：引擎写死单线程领取（服务端在途上限低分 2 / 高分 3，
> 单线程排队正好匹配）。

界面还提供：运行状态、USB 挂载点列表、测试连接、查看日志、清空缓存、重启引擎。

## 5. 协议对齐

完整细节见 [`docs/01-协议对齐-软总线-解析桥-代理桥.md`](docs/01-协议对齐-软总线-解析桥-代理桥.md)。

- **软总线**：`GET /api/m/remote/pending`（长轮询 + 心跳，60 s 无心跳离线）、
  `POST /api/m/remote/result`；引擎写死 `limit=1`、单线程处理；
- **解析桥**：`POST /api/m/ebook/part?sha1&seq&total` 分片上传，part0 清单、正文片 ≤ 50 章
  或约 2 MB；`GET /api/m/ebook/parts` 断点续传；`GET /api/m/ebook/file/<sha1>` 下载；
  本插件 0.3 起不再领取 `ebook-*` 任务，代码保留；
- **代理桥**：`ebookMode=proxy` 下由服务端派单，超时/掉线由服务端本机兜底，
  本引擎只做节点，不抢服务端状态机；
- **书源规则**：`GET /api/m/sources.json` 拉书源列表（缓存 10 分钟），按
  payload 的 `source`（bookSourceUrl）取 `ruleBookInfo` 解析详情页；规则子集见
  [docs/01](docs/01-协议对齐-软总线-解析桥-代理桥.md)；
- **2.6.17/18 路由**：服务端按隐藏性能分派单（初始 50，成功 +4、失败 −8、慢请求再扣 2）。
  `<55` 的低分设备只派 `media`（`cover`/`intro`/`bookinfo`）。本插件不伪造分数，
  只领 `media`、对误派的 `book`/`content` 立即回传 `unsupported`（−8 分），
  因此分数无法稳定维持高分，等效隐藏分封顶 50。领取即锁 30 秒，同一任务重复回传按
  `duplicate` 处理；
- **结果幂等**：`/remote/result` 返回 `ok:"duplicate"` / `"stale"`（非锁定设备）/
  `ignored`（调试设备）均视为终态，不再重试；
- **契约**：`fnreader.ebook.v1`，`k` 仅 `p/h1~h6/quote/hr/img`，`index` 全书连续，
  `total = 1 + 正文片数`。

## 6. 解析能力

| 格式 | 支持 | 说明 |
| --- | --- | --- |
| EPUB | ✅ | zip + OPF + EPUB3 nav / EPUB2 NCX，DRM 检测 |
| MOBI / PRC | ✅ | PalmDOC 压缩 1/2、EXTH、h1-h3 分章；KF8 返回 `kf8-unsupported` |
| PDF | ✅ | Flate 流 + ToUnicode；扫描件无文本层返回 `no-text-layer` |
| TXT | ✅ | UTF-8/GBK/UTF-16 嗅探，「第 X 章/回/节」等章节规则 |
| Web 详情页 | ✅ | `cover`/`intro`/`bookinfo`：书源 `ruleBookInfo` 规则优先（class/id/tag/@css、属性、`##正则##`、`||` 多规则），og/meta、JSON-LD、封面 img、简介区块兜底 |

错误码与服务端一致：`drm` / `corrupt` / `unsupported` / `kf8-unsupported` /
`no-text-layer` / `font-encoding` / `timeout`；媒体任务额外使用 `fetch` / `not-found`，
失败时回 `ok:false`，由服务端回退 `bookinfo` 或本机抓取。
`unsupported` 也用于拒绝非媒体类任务，服务端据此扣分并兜底。

## 7. 缓存

```
<cache_dir>/
├── books/     原书（按 sha1 命名，Range 续传）
├── parsed/    解析结果 JSON.gz（单章补章秒回）
├── tmp/       分片/临时文件
├── sources.json  服务端书源缓存（media 任务用，TTL 10 分钟）
├── state.json 运行状态（LuCI 读）
└── device.id  设备标识兜底
```

- 只选真实挂载的可写文件系统（ext4/vfat/exfat/ntfs，`/dev/sd*`、`/dev/mmcblk*`）；
  黑名单 `/`、`/tmp`、`/overlay`；
- 每小时清理：过期原文件 + 超上限按最旧淘汰；
- 缓存不可用时任务直接失败并回 `io`，不会写 flash 兜底。

## 8. 目录结构

```
engine/                 Go 引擎源码（纯 stdlib）
  parse/                epub/mobi/pdf/txt 解析器 + rulex.go 书源规则引擎
  sources.go            书源列表缓存（/api/m/sources.json）
  assets/cacert.pem     内嵌 CA
openwrt/
  files/                配置、init、rpcd 脚本
  luci-app-fnparse/     LuCI menu/acl/view
scripts/
  build_pkg.py          交叉编译 + 打 ipk/apk
  mock_server.py        本地模拟 legado-web，端到端验收
  gen_gbk.py            GBK 表生成
dist/                   稳定版产物（0.3）
  old/                  历史 0.1 / 0.2 包
docs/                   协议对齐文档
```

## 9. 构建

需要 Go 1.21+（本仓库用 1.24.9 验证）。

```powershell
# Windows：先设 GOROOT/GOCACHE/TMP 到空间充足的盘
python scripts/build_pkg.py --build
```

产物：

```
dist/fnparse_0.3_armv7.ipk
dist/fnparse_0.3_arm64.ipk
dist/fnparse_0.3_armv7.apk
dist/fnparse_0.3_arm64.apk
dist/SHA256SUMS
```

单独编译：

```sh
cd engine
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags "-s -w" -o fnparse-armv7 .
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o fnparse-arm64 .
```

## 10. 验收记录（0.3）

- `go test ./parse` 通过：HTML 块规则、实体、GBK、og/meta 与 JSON-LD 详情页提取，
  以及书源规则引擎（class/id/tag/@css 选择器、属性取值、`##正则##`、`||` 多规则、
  `{{}}` 占位、init 地址换算、sources.json 解析）；
- `go test .` 通过：详情页 HTTP 抓取、`cover`/`intro` 任务回传、result 幂等（duplicate/stale/ignored）；
- `go vet ./...` 无告警；
- ARMv7 / ARM64 交叉编译产物经 ELF 头校验（machine 0x28 / 0xB7）；
- 打包脚本产出 0.3 版 ipk/apk，`scripts/verify_pkg.py` 校验版本、架构、领取类型全部通过。

## 11. 已知限制

- KF8 / AZW3 不解析，按协议回 `kf8-unsupported`（0.3 起不再领取 `ebook-*`，代码保留）；
- PDF 扫描件无文本层、CID 字体乱码分别回 `no-text-layer` / `font-encoding`
  （路由器通常没有 ghostscript）；
- MOBI 只支持 PalmDOC 压缩 1/2，不处理 HUFF/CDIC；
- apk 包未签名，必须 `--allow-untrusted`；
- `cover` / `intro` / `bookinfo` 只解析静态 HTML；JS 动态渲染或需登录的详情页可能取不到，
  失败回 `ok:false` 由服务端回退；
- 书源规则只支持子集：`@js:` / `@json:` / `@put:` / `@get:` 等表达式类规则整段放弃
  （自动回退通用网页解析）；`ruleBookInfo.init` 只识别能换算成 http(s) 地址的写法；
- 服务端隐藏分由服务端内部维护，客户端无法直接写死；本插件通过「只领 media +
  对误派任务回传失败」把分数压在低分档，等效封顶 50，但不保证服务端侧数字恒等于 50；
- 不实现 `search` / `toc` / `content` / `rank` 与 `ebook-*` 领取（本插件定位只做简介/封面）。

## 12. 版本

| 版本 | 日期 | 内容 |
| --- | --- | --- |
| 0.1 | 2026-11-15 | 首个稳定版：软总线节点 + EPUB/MOBI/PDF/TXT 解析 + LuCI + USB 缓存 + ipk/apk |
| 0.2 | 2026-11-17 | 适配服务端 2.6.17/18：cover/intro/bookinfo 媒体任务、隐藏分路由说明、结果幂等 duplicate/stale/ignored |
| 0.3 | 2026-11-17 | 书源规则解析（ruleBookInfo 子集）、写死单线程领取、只承接简介/封面、隐藏分等效封顶 50 |

## License

MIT
