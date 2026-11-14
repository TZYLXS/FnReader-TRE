# FnReader-TRE

**FnReader 软总线解析引擎（OpenWrt / LEDE / ARM）**

版本：**0.1（稳定版）**

把 FnReader 电子书解析链路里的「解析桥」落到路由器上：设备作为软总线节点长轮询领任务，
下载原书、解析成 `fnreader.ebook.v1` 分片回传，服务端本机解析作为兜底保持不变。
附 LuCI 管理界面，安装包内自带全部运行环境（纯静态 Go 二进制，零第三方依赖）。

---

## 1. 它解决什么问题

FnReader（legado-web）默认由服务端或手机 App 解析电子书。TRE 把这件事交给常年在线、
功耗低的路由器：

- 服务端派 `ebook-parse` / `ebook-chapter` 任务，路由器领取并执行；
- 解析结果按协议分片上传，服务端逻辑、书库数据完全不用改；
- 解析缓存与下载的原文件放在 USB，不写路由器 flash；
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
opkg install --force-architecture /tmp/fnparse_0.1_armv7.ipk

# apk（OpenWrt 25.12+ / apk 分支）
apk add --allow-untrusted /tmp/fnparse_0.1_armv7.apk
```

安装后：

1. 浏览器打开 LuCI → **服务 → FnReader 解析引擎**；
2. 填「接入 IP / 服务端地址」（如 `http://192.168.5.110:1122`）和 Token；
3. 插 U 盘，点「刷新状态」确认检测到 USB；缓存目录留空即自动选盘；
4. 保存后点「重启引擎」，状态变绿即上线；
5. 服务端「设备」页应看到 `owrt-xxxxxxxxxx` 节点在线。

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
| 并发解析数 | 2 | 1-8，弱路由建议 1-2 |
| 长轮询等待（秒） | 20 | 服务端上限 30 |
| 分片上限（MB） | 2 | 50 章或该字节数切分，不拆章 |
| 缓存上限（MB） | 2048 | 超出按最旧清理 |
| 原文件保留（小时） | 48 | 过期随清理删除 |
| 日志级别 | info | debug/info/warn/error |
| 领取任务类型 | `ebook-parse,ebook-chapter` | 逗号分隔 |

界面还提供：运行状态、USB 挂载点列表、测试连接、查看日志、清空缓存、重启引擎。

## 5. 协议对齐

完整细节见 [`docs/01-协议对齐-软总线-解析桥-代理桥.md`](docs/01-协议对齐-软总线-解析桥-代理桥.md)。

- **软总线**：`GET /api/m/remote/pending`（长轮询 + 心跳，60 s 无心跳离线）、
  `POST /api/m/remote/result`；
- **解析桥**：`POST /api/m/ebook/part?sha1&seq&total` 分片上传，part0 清单、正文片 ≤ 50 章
  或约 2 MB；`GET /api/m/ebook/parts` 断点续传；`GET /api/m/ebook/file/<sha1>` 下载；
- **代理桥**：`ebookMode=proxy` 下由服务端派单，超时/掉线由服务端本机兜底，
  本引擎只做节点，不抢服务端状态机；
- **契约**：`fnreader.ebook.v1`，`k` 仅 `p/h1~h6/quote/hr/img`，`index` 全书连续，
  `total = 1 + 正文片数`。

## 6. 解析能力

| 格式 | 支持 | 说明 |
| --- | --- | --- |
| EPUB | ✅ | zip + OPF + EPUB3 nav / EPUB2 NCX，DRM 检测 |
| MOBI / PRC | ✅ | PalmDOC 压缩 1/2、EXTH、h1-h3 分章；KF8 返回 `kf8-unsupported` |
| PDF | ✅ | Flate 流 + ToUnicode；扫描件无文本层返回 `no-text-layer` |
| TXT | ✅ | UTF-8/GBK/UTF-16 嗅探，「第 X 章/回/节」等章节规则 |

错误码与服务端一致：`drm` / `corrupt` / `unsupported` / `kf8-unsupported` /
`no-text-layer` / `font-encoding` / `timeout`，这些服务端直接 failed；其余网络类错误
服务端本地兜底。

## 7. 缓存

```
<cache_dir>/
├── books/     原书（按 sha1 命名，Range 续传）
├── parsed/    解析结果 JSON.gz（单章补章秒回）
├── tmp/       分片/临时文件
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
  parse/                epub/mobi/pdf/txt 解析器
  assets/cacert.pem     内嵌 CA
openwrt/
  files/                配置、init、rpcd 脚本
  luci-app-fnparse/     LuCI menu/acl/view
scripts/
  build_pkg.py          交叉编译 + 打 ipk/apk
  mock_server.py        本地模拟 legado-web，端到端验收
  gen_gbk.py            GBK 表生成
dist/                   稳定版产物（0.1）
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
dist/fnparse_0.1_armv7.ipk
dist/fnparse_0.1_arm64.ipk
dist/fnparse_0.1_armv7.apk
dist/fnparse_0.1_arm64.apk
dist/SHA256SUMS
```

单独编译：

```sh
cd engine
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags "-s -w" -o fnparse-armv7 .
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o fnparse-arm64 .
```

## 10. 验收记录（0.1）

- Windows 本机 `go build` 通过；`go vet` 无告警；parse 包单测通过（HTML 块规则、实体、GBK）；
- 真实样本：EPUB（3 章）、MOBI《水浒传-三章》（3 章，UTF-8 嗅探修正）、PDF（1 节）、
  TXT（1080 字）均解析成功；
- 端到端联调（`scripts/mock_server.py` 模拟 legado-web）：pending 领任务 → 下载 →
  解析（命中缓存）→ part0/partN 上传 → `remote/result` 返回
  `{ok:true, chapters:3, parts:2}`，全链路 35 ms；
- ARMv7 / ARM64 交叉编译产物经 ELF 头校验（machine 0x28 / 0xB7）；
- 打包脚本产出 ipk/apk 并生成 SHA256SUMS。

## 11. 已知限制

- KF8 / AZW3 不解析，按协议回 `kf8-unsupported`；
- PDF 扫描件无文本层、CID 字体乱码分别回 `no-text-layer` / `font-encoding`
  （路由器通常没有 ghostscript）；
- MOBI 只支持 PalmDOC 压缩 1/2，不处理 HUFF/CDIC；
- apk 包未签名，必须 `--allow-untrusted`；
- 解析质量以服务端 `fnreader.ebook.v1` 为准，个别复杂排版可能与手机端略有差异。

## 12. 版本

| 版本 | 日期 | 内容 |
| --- | --- | --- |
| 0.1 | 2026-11-15 | 首个稳定版：软总线节点 + EPUB/MOBI/PDF/TXT 解析 + LuCI + USB 缓存 + ipk/apk |

## License

MIT
