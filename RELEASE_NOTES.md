# FnReader-TRE 0.3

**2026-11-17 · 书源规则解析 + 只承接简介/封面，适配 FnReader 服务端 2.6.17 / 2.6.18**

OpenWrt / LEDE / ARM 软总线解析桥，纯静态 Go 二进制，LuCI 管理，解析缓存放 USB。

## 本版变更

### 书源解析接入（media 任务）

- `cover` / `intro` / `bookinfo` 不再只靠通用网页解析：payload 的 `source`
  （bookSourceUrl）命中服务端 `/api/m/sources.json`（本地缓存 10 分钟）时，
  用该源的 `ruleBookInfo` 规则解析详情页。
- 规则子集（`engine/parse/rulex.go`）：
  - `||` 多段依次尝试，取第一个非空结果；
  - `@` 分步：`class.` / `id.` / `tag.` / `@css:`（tag、.class、#id、[attr=val]、
    后代与子代组合器）选择器 → `text` / `textNodes` / `ownText` / `all` / `html`
    或任意属性名（`src` / `href` / `data-src` …）取值 → 可选 `##正则##`，
    连续两个 `##` 为替换形式；
  - `{{bookUrl}}`、`{{name}}`、`{{author}}` 占位替换；
  - `ruleBookInfo.init` 能换算成 http(s) 地址时按其抓取详情页，否则用 `bookUrl`。
- 规则不命中 / 字段为空时自动回退 og/meta、JSON-LD、封面 img、简介区块等通用解析，
  只补空字段；`@js:` / `@json:` / `@put:` / `@get:` 表达式类规则整段放弃。

### 领取策略写死

- **单线程**：worker 数写死 1，pending 每次 `limit=1`，处理完再领下一个；
- **只领 media 类**：`cover` / `intro` / `bookinfo`；`claim_types` 由引擎强制写死，
  UCI 里的其它取值被忽略；
- **隐藏分等效封顶 50**：服务端隐藏分（成功 +4 / 失败 −8）由 server.js 内部维护，
  协议没有字段让客户端直接写死。插件通过「只领 media + 对误派的 `book`/`content`
  立即回传 `ok:false, unsupported`（−8 分）+ 单线程控制加分频率」把分数压在低分档，
  无法稳定维持高分，效果等同于封顶 50。
- `ebook-parse` / `ebook-chapter` 代码保留但不再领取；0.2 的 `concurrency` 配置废弃，
  升级脚本自动删除。

### 配置与界面

- UCI 默认 `claim_types 'cover,intro,bookinfo'`（展示用，引擎侧写死）；
- LuCI 移除「并发解析数」，更新任务类型说明；
- 升级脚本：旧配置含 `ebook-*` 时强制改为媒体三类，并清理 `concurrency`。

## 产物

| 文件 | 大小 | SHA256 |
| --- | --- | --- |
| `fnparse_0.3_armv7.ipk` | 2.8 MB | `8cfc9d66da0c835041d549eb791058562b1aaf315656a3c172d790e92eb4511a` |
| `fnparse_0.3_armv7.apk` | 2.8 MB | `157db7e70608133022894b1588351ab4e9eaddfde442f16b261d580aaedd1174` |
| `fnparse_0.3_arm64.ipk` | 2.7 MB | `cc32f9b985fe290965678be0af1a1b93ae9cd28923a225cf631324385dade2df` |
| `fnparse_0.3_arm64.apk` | 2.7 MB | `211a481f1507bfd1393cc9438d8cb70f84f1cd2eb39ec498f33c0e01c9e30f09` |

安装：

```sh
opkg install --force-architecture /tmp/fnparse_0.3_armv7.ipk
# 或 OpenWrt apk 分支
apk add --allow-untrusted /tmp/fnparse_0.3_armv7.apk
```

## 校验

- `go test ./...` 通过（新增 rulex 规则引擎用例：选择器、属性、正则、`||`、占位、
  init 换算、sources.json 解析）；
- `go vet ./...` 无告警；
- 四包版本 0.3、ELF 架构 ARM/AArch64、领取类型校验通过（`python scripts/verify_pkg.py`）。

## 已知限制

- 书源规则只支持上述子集；JS 动态渲染、需登录/反爬的详情页取不到时回
  `ok:false` 由服务端兜底；
- 服务端隐藏分无法从客户端真写死，本版实现的是等效封顶（见上）；
- 不实现 `search` / `toc` / `content` / `rank`，不再领取 `ebook-*`。

## 历史版本

- **0.2**：cover/intro/bookinfo 媒体任务、隐藏分路由说明、结果幂等
  duplicate/stale/ignored（四包 SHA 见 `dist/old/` 或 tag `v0.2`）。
- **0.1**：首个稳定版：软总线节点 + EPUB/MOBI/PDF/TXT 解析 + LuCI + USB 缓存。
