# OAIprism

OpenAI Prism 的高性能反向代理。把 `prism.openai.com` 的内部 start + poll 协议
包装成标准的 **OpenAI Chat Completions / Responses** 与 **Anthropic Messages** 接口，
同时保留一条完全不懂协议也能用的原样反代通道。

Go 单二进制，零 CGO，无运行时依赖。

---

## 系统架构

```
                              ┌──────────────────────────────────────────────┐
   OpenAI SDK / Codex CLI ──► │  8787  OAIprism 网关（Go 单二进制）           │
   Anthropic SDK          ──► │  ├─ /v1/*          OpenAI/Anthropic 门面      │
   curl / 任意 HTTP 客户端 ──► │  │   （chat/completions、responses、messages）│
                              │  ├─ /prism/*       原样反代通道（白名单）      │
                              │  ├─ /admin/*       管理 API（账号/日志/会话/  │
                              │  │                 API Key/OAuth，见「端点一览」）│
                              │  ├─ /dashboard/    Dashboard 静态资源（web/dist）│
                              │  ├─ /healthz /readyz /metrics                  │
                              │  └─ /chat/completions 等无前缀别名（兼容老客户端）│
                              └───────────────┬──────────────────────────────┘
                                              │ 调优 HTTP 客户端（强 HTTP/2、
                                              │ 连接预热、往返 0 拷贝）
                                              ▼
                              ┌──────────────────────────────────────────────┐
                              │  8790  Go TLS 桥（oaiprism tlsbridge）        │
                              │  ├─ Go 侧：Chrome 系 TLS 指纹传输 + 轮询编排   │
                              │  └─ 每请求 POST 8791 /token 取一次性 Sentinel │
                              └───────────────┬──────────────────────────────┘
                                              │ 带 openai-sentinel-token
                                              ▼
                              ┌──────────────────────────────────────────────┐
                              │  8791  Sentinel token oracle（node + Chrome） │
                              │  真实页面环境执行 SentinelSDK.token()          │
                              │  只做签发（~100ms），不转发任何数据面流量       │
                              └───────────────┬──────────────────────────────┘
                                              ▼
                                  prism.openai.com（Cloudflare 之后）
```

**一次推理的数据流**：客户端 → 8787 门面（协议翻译成内部 start+poll）
→ 8790 桥（补 Sentinel token、按 Chrome 指纹发请求）→ 上游 start
→ 8787 轮询 status（经 8790）→ 增量转 SSE 推回客户端 → 收尾写入 usage 估算。

**为什么必须走桥**：Cloudflare 会拦截不带浏览器级信任特征的裸请求；
8790/8791 组合把"传输指纹"与"反自动化令牌"分开解决 —— oracle 只签发
（真实页面里跑 SDK），桥承担全部数据面，崩溃面小、可独立重启。

### 端口分配

| 端口 | 服务 | 归属 | 说明 |
|---|---|---|---|
| **8787** | OAIprism 网关 | `oaiprism serve` | 对外唯一入口：API（`/v1/*`、`/prism/*`、`/admin/*`）+ Dashboard（`/dashboard/`）+ 探针与指标 |
| **8790** | Go TLS 桥 | `oaiprism tlsbridge` | 仅本机监听；网关的 `upstream.base_url` 指向它 |
| **8791** | Sentinel token oracle | `tools/sentinel_oracle.js` | 仅本机监听；只提供 `POST /token` 与 `GET /healthz` |

> Dashboard 没有独立端口，由网关在 `/dashboard/` 伺服（访问 `/dashboard`
> 会 302 补斜杠）。桥模式（`8790`）是生产默认；仅做只读探测时可把
> `upstream.base_url` 直连 `https://prism.openai.com`，但没有 Sentinel
> token 的写操作会被上游 403 拒绝。详见 `configs/config.example.yaml`
> 的 upstream 注释。

---

## 为什么是这个技术栈

| 需求 | 选择 | 理由 |
|---|---|---|
| 语言 | Go 1.26 | 流式反代的瓶颈是"连接多、分配少、延迟低"，Go 的 net/http + goroutine 模型恰好命中；单二进制部署，无运行时依赖 |
| 上游连接 | 标准库 `http.Transport` | 原生支持 HTTP/2 多路复用；`fasthttp` 不支持 HTTP/2 上游，反而更慢 |
| 缓冲 | `sync.Pool` + `bufio` | 稳态零分配；SSE 逐帧 `Flush` |
| JSON | 手写编码器 | 热路径上避开反射与 map 分配，单 chunk 编码开销下降一个数量级 |
| 指标 | 自研 Prometheus 文本输出 | 只用了十几个指标，不值得引入 5 个间接依赖 |
| 依赖总数 | **5 个直接依赖** | 供应链面小到可以人工审计 |

直接依赖清单（`go.mod`，全部有明确不可替代性）：

| 依赖 | 用途 | 为什么不自己写 |
|---|---|---|
| `gopkg.in/yaml.v3` | 配置解析 | 配置文件的既定格式 |
| `github.com/bogdanfinn/tls-client`（+ `fhttp`） | 桥的 Chrome 系 TLS 指纹传输 | 指纹细节（ciphersuite 顺序、扩展、ALPN）无法用标准库表达 |
| `modernc.org/sqlite` | Dashboard 持久化（账号/会话/请求日志） | 纯 Go 实现，**保持零 CGO**；database/sql 驱动接口 |
| `golang.org/x/time` | 令牌桶限流 | `rate.Limiter` 是标准做法，自写只会写错 |

退出标准：新增依赖必须能回答"标准库为什么不够"。指标、SSE、JSON
编码、限流闸门、直方图仍为自研。

---

## 快速开始

### 1. 编译

```bash
go build -o oaiprism.exe ./cmd/oaiprism
```

### 2. 准备凭据（你唯一需要提供的东西）

**推荐方式**：一条命令搞定，顺带在线校验。

```bash
# 从浏览器开发者工具复制整串 Cookie 后（Prism 的登录 cookie）：
./oaiprism.exe import -cookie "prism_oai_access_token=eyJ...; prism_session_token=..." -id main

# 只给 access token 的值（Prism 的 JWT，约 10 天有效）：
./oaiprism.exe import -access-token "eyJhbGci..." -id main

# 只给会话 cookie 的值：
./oaiprism.exe import -session-token "eyJ..." -id main

# 有 refresh_token 最省心（约 90 天有效，可自动续期）：
./oaiprism.exe import -refresh-token "rt.1..." -id main

# 或者从标准输入读，避免 shell 历史泄漏：
./oaiprism.exe import -stdin -id main

# 也可以走官方 OAuth 授权（浏览器回调，无需手工复制 cookie）：
#   启动后在 Dashboard「账号」页点「官方授权导入」，
#   或调用 POST /admin/oauth/begin 按提示完成。
```

命令会做四件事：向上游验证凭据 → 补全 email/plan/account_id →
（若可能）换取 refresh_token 以获得自愈能力 → 写入 `secrets/accounts.json`。

> 凭据从哪拿：登录 `prism.openai.com`，打开开发者工具 → Application →
> Cookies，复制 `prism_oai_access_token`（真正的 Bearer JWT）与
> `prism_session_token`；或 Network 面板里任意请求的完整 Cookie 头。
> 注意 Prism 用的是自家 cookie 体系（`prism_oai_*` / `prism_session_token`），
> **不是** next-auth 的 `__Secure-next-auth.session-token`。

也可以直接编辑 `secrets/accounts.json`（参考 `secrets/accounts.example.json`）。
**服务运行中保存该文件即自动生效，5 秒内热加载，无需重启。**

### 3. 启动

生产链路需要**三个服务**（顺序：oracle → 桥 → 网关）。

**一键启动（推荐）**：

```powershell
tools\start_bridge.ps1     # 或 tools\start_bridge.cmd（cmd 环境）
tools\stop_bridge.ps1      # 停止三件套（含自动化 Chrome 清理）
tools\check_bridge.cmd     # codex 报错时先跑这个诊断
```

脚本行为：检查端口占用 → 缺则从仓库重新构建 `oaiprism.exe` →
依次拉起 8791 oracle / 8790 桥 / 8787 网关 → 逐一健康检查并打印结果。
日志在 `%TEMP%`（`oracle.log` / `tlsbridge.log` / `oaiprism_8787.log`）。

> 脚本里的 `REPO` / `NODE` 路径是本机约定，换机器需要改脚本头部三行。

**手动启动（等价，便于排障）**：

```bash
go build -o oaiprism.exe ./cmd/oaiprism

# 1) Sentinel oracle（会打开一个 Chrome 窗口，属正常）
node tools/sentinel_oracle.js auto 8791

# 2) Go TLS 桥
./oaiprism.exe tlsbridge -port 8790 -oracle http://127.0.0.1:8791 -accounts secrets/accounts.json

# 3) 网关
cp configs/config.example.yaml configs/config.yaml
./oaiprism.exe serve -config configs/config.yaml
```

**仅网关模式（离线调试 / 契约测试）**：跳过 8790/8791，直接
`./oaiprism.exe serve -config configs/config.yaml` —— 只读端点可用，
需要 Sentinel 的写操作会 403。要完全离线跑，可把上游指向
`tools/mock_upstream.py` 提供的模拟服务。

没配凭据也能正常启动——服务会打印明确警告，等你把凭据放进去后自动开始工作。

### 4. 用起来

```bash
# OpenAI 风格（默认模型 gpt-6.1-sol，可选 gpt-6-luna / gpt-5.6-sol 等）
curl http://127.0.0.1:8787/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-6.1-sol","stream":true,"messages":[{"role":"user","content":"你好"}]}'

# 直接指向官方 SDK
export OPENAI_BASE_URL=http://127.0.0.1:8787/v1
export OPENAI_API_KEY=sk-oaiprism-your-secret

# Anthropic 风格
export ANTHROPIC_BASE_URL=http://127.0.0.1:8787

# 多轮会话：同一 X-Oaiprism-Session 头 = 同一会话（项目复用 + 历史折叠）
curl http://127.0.0.1:8787/v1/chat/completions \
  -H "X-Oaiprism-Session: my-chat-1" -H "Content-Type: application/json" \
  -d '{"model":"gpt-6.1-sol","stream":false,"messages":[{"role":"user","content":"记住暗号：蓝莓芝士"}]}'

# 原样反代（不懂协议也能用）
curl http://127.0.0.1:8787/prism/api/auth/session

# Dashboard（账号管理 / 会话调试 / 请求统计）
# 浏览器打开 http://127.0.0.1:8787/dashboard/
```

---

## 端点一览

**推理门面**（`facade.enabled: true` 时注册；另有 `/chat/completions`、
`/responses`、`/models` 三个无前缀别名，兼容老客户端）：

| 端点 | 说明 |
|---|---|
| `POST /v1/chat/completions` | OpenAI Chat Completions（流式/非流式、多模态附件） |
| `POST /v1/completions` | 老式 Completions，内部转 chat |
| `POST /v1/responses` | OpenAI Responses API（新版 SDK / Codex CLI 首选；含工具桥模式） |
| `POST /v1/messages` | Anthropic Messages（含完整流式事件序列） |
| `GET /v1/models`、`GET /v1/models/{id}` | 模型清单（唯一真相源，含各档位变体） |
| `GET /v1` | 索引页（列出可用端点） |
| `POST /v1/embeddings`、`POST /v1/images/generations` | 明确 501 —— 上游只提供对话式补全 |

**运维与探针**：

| 端点 | 说明 |
|---|---|
| `GET /healthz` | 存活探针（不检查下游，避免上游抖动导致误重启） |
| `GET /readyz` | 就绪探针（无可用账号时明确返回 503） |
| `GET /metrics` | Prometheus 文本指标 |
| `GET /dashboard/` | Dashboard（访问 `/dashboard` 会 302 补斜杠；根路径 `/` 未注册） |

**管理 API**（`/admin/*`）：

| 端点 | 说明 |
|---|---|
| `GET /admin/accounts` | 账号池状态（不含凭据） |
| `POST /admin/accounts`、`PUT /admin/accounts/{id}`、`DELETE /admin/accounts/{id}` | 账号增删改 |
| `POST /admin/accounts/{id}/refresh` | 强制刷新某个账号的 token |
| `POST /admin/reload` | 手动重载凭据文件 |
| `POST /admin/oauth/begin`、`GET /admin/oauth/status`、`GET /admin/oauth/callback`、`POST /admin/oauth/exchange` | 官方 OAuth 授权导入（PKCE，localhost 回调） |
| `GET /admin/requests` | 请求明细（含模型/账号绑定、耗时） |
| `GET /admin/statistics` | 调用统计 |
| `GET /admin/stats` | 运行态汇总 |
| `GET/POST /admin/chat/sessions`、`DELETE /admin/chat/sessions/{id}`、`GET/POST /admin/chat/sessions/{id}/messages` | Dashboard 会话 CRUD（SQLite 持久化） |
| `GET/POST /admin/apikeys`、`DELETE /admin/apikeys/{key}` | 调用方 API Key 管理 |
| `POST /admin/login`、`GET /admin/me` | Dashboard 登录态 |

**原样反代**：`/prism/*`（前缀可配）按白名单转发到上游，见「安全要点」。

> 完整注册表见 `internal/server/server.go` 与 `internal/facade/http.go`；
> 任何一端点变更都应在两处同步。

### 请求级控制头

| 头 | 作用 |
|---|---|
| `X-Oaiprism-Session` | 指定会话身份 → 决定账号粘性与项目复用 |
| `X-Oaiprism-Account` | 强制使用某个账号（调试用） |
| `X-Oaiprism-Project` | 强制使用某个上游项目 ID |
| `X-Oaiprism-Model` | 覆盖模型名 |
| `X-Oaiprism-Effort` | 覆盖推理强度 |
| `X-Oaiprism-Previous` | 显式传入上一轮 `response_id`（续接用） |

---

## 性能设计

反代的性能瓶颈不在"算得快"，而在"连接复用、内存分配、延迟首字节"。
下面是具体做了什么：

**连接层**
- `MaxIdleConnsPerHost: 256`（默认 2 是吞吐的第一瓶颈）
- 启动时预热 TCP + TLS，把 DNS/握手成本挪出请求路径
- 出站强制 HTTP/2 多路复用
- 关闭 Go 的自动 gzip，否则 Content-Encoding/Length 会与实际不一致

**流式**
- SSE 缓冲来自 `sync.Pool`，稳态零分配
- 手写 JSON 转义（对照标准库做了等价性测试）
- 每个增量立即 `Flush`，并显式设置 `X-Accel-Buffering: no`，
  否则 Nginx 会把整个流缓冲到结束，"流式"退化成一次性返回

**轮询转流式**

上游是 start + poll 协议，天生不是流式的。这里做了三件事把它压成流式：
1. **可选长轮询**：配 `use_status_wait: true` 后带 `waitMs`（默认
   10000），有内容立刻返回、没内容挂起。当前本地配置为 `false`
   （纯轮询 + 退避），两种模式的代码路径都有测试。
2. **自适应退避**：若上游忽略 `waitMs`（响应耗时 < 250ms），自动退避，
   不会退化成忙轮询；长轮询生效时则不叠加 sleep。
3. **前缀差分**：不管上游返回累计全文还是结构化消息列表，
   用前缀差分还原 token 级增量，因此不依赖具体响应形态。

**项目复用**

`POST /api/projects` 比推理调用本身还慢。同一会话只建一次工程并长期复用
（`reuse_project: true`）；无会话标识的请求走轮转分桶，避免所有请求挤在
一个项目里被上游串行化。复用键就是下面的 `StickyKey`：显式
`X-Oaiprism-Session` 头 > 请求体 `user` 字段 > 首条消息指纹。

**可观测**

`oaiprism_facade_first_delta_seconds` 直接量首字延迟——这是流式体验的核心指标，
比端到端耗时更能反映体验好坏。

---

## 协议（已实测校准）

> **状态：已用真实流量校准。** 详见 [`docs/协议校准报告.md`](docs/协议校准报告.md)。
> 三处独立证据交叉验证（上游错误信息、前端 bundle 源码、可复现的路径对照），
> 不是推断。

### 关键路径：是 `llm` 不是 `lim`

早期那版逆向文档写成 `/api/lim/response_with_tools_start`，**是笔误**：

| 路径 | 结果 |
|---|---|
| `/api/llm/response_with_tools_start` | JSON API（假 token 得到 401，说明路由与鉴权都正常） |
| `/api/lim/response_with_tools_start` | Next.js 的 404 HTML 页（和随便编的路径完全一样） |

决定性证据是前端源码：`grep "api/lim" *.js` 命中 **0** 次，
而 `/api/llm/response_with_tools_{start,status,stop}` 三个全在。
E2E 测试里有一条断言：一旦有请求打到 `/api/lim/`，测试立刻失败。

### 协议形状

```jsonc
// POST /api/llm/response_with_tools_start
{"input":[{"type":"message","role":"user",
           "content":[{"type":"input_text","text":"…"}]}],
 "previousResponseId":"req-…",          // 可选，camelCase
 "conversationId":"…",                  // 可选，camelCase
 "metadata":{"model":"gpt-5.6-sol",     // ← 模型参数在 metadata 里，不在顶层
             "reasoning_effort":"medium",
             "projectId":"…",
             "frontend_origin":"https://prism.openai.com"}}

// → {"status":"started","request_id":"…","turn_state":{…}}      需要轮询
// → {"status":"completed","request_id":"…","response":{…}}      已经结束

// POST /api/llm/response_with_tools_status
//   必须带 {request_id, turn_state} 两个字段
//   turn_state 是服务端下发的不透明续令牌，必须原样回传（自造值会被拒）
// → {"status":"pending","turn_state":{…新令牌…}}                继续轮询
// → {"status":"completed","response":{"status":"success","payload":{"output":[…],…}}}

// 答案位置：response.payload.output[-1].content[*].text
// 只取最后一条 output —— 多轮下 output 会累积历史，全拼会把上一轮当这一轮。
```

**三个反直觉的点，每个都是一个坑**：

1. **失败是 HTTP 200**。上游用 `status:"completed" + response.status:"error"`
   表达失败。只看状态码会把失败当成"成功但内容为空"，返回一个空回答给客户端。
2. **`turn_state` 是续令牌**，每轮换新；忘了更新就会永远拿到同一个 `pending`。
3. **大小写不统一**：start 用 camelCase 的 `conversationId`，
   status 用 snake_case 的 `request_id`。不要"顺手统一"。

### 轮询节奏的取舍

真实前端是**固定 5 秒**轮询，且 pending 分支只读 `turn_state` 不看 `response`
—— 这暗示 pending 帧可能根本没有正文。我们默认取 1 秒作为折中，
并且循环内做了**无条件节流**（绝不会退化成零间隔忙轮询）。
出现 429 就调大 `poll_interval`。

### 已定论与仍待验证

| 项 | 结论 |
|---|---|
| OpenAI 的 `tools` 该映射到哪 | **已定论**：上游是 server-side tools 架构（`response_with_tools_*`，工具循环在沙箱内消化，终态 output 只有 message），客户端 `tools` 无意义 —— 本地 Codex CLI 的文件操作经**工具桥**（`codex-exec` 块由 CLI 本地执行）实现，见 `internal/facade/toolbridge.go` |
| 上游可用模型列表 | **已内建映射表**（`GET /v1/models` 是唯一真相源）：4 主模型 × 档位 = 14 项 —— `gpt-6.1-sol`（默认，含 low/high/xhigh）、`gpt-6-luna`（high/xhigh）、`gpt-5.6-sol`（low/high/xhigh）、`gpt-5.6-terra`（high/xhigh），见 `internal/config/config.go` |
| `pending` 帧里是否有累计正文 | **仍待验证** —— 两种形态都已支持并有测试 |
| 多轮上下文机制 | **已实测定稿**，见下节「多轮上下文与上下文窗口」 |

### 字段名仍是可配置的

上面的值已经写进 `facade.schema` 的默认值，同时保留配置能力以应对上游变化。
改字段不需要重新编译。校准流程：

```bash
# 抓包摘要在协议考古期使用（capture.enabled: true 时才有新抓包文件）
./oaiprism.exe capture-summary -file captures/capture-<日期>.jsonl
```

## 多轮上下文与上下文窗口（2026-10-02 实测定稿）

这一节的所有结论都来自**真实请求对照实验**与 **playwright 驱动真实
浏览器考古**（脚本与原始数据在 `tools/webui_probe*.js`、`tools/probe_context_limit.py`），
不是推断。

### 上游的两个硬事实

1. **单次请求只处理「首条 system + 最后一条 user」**，中间的 input
   条目全部丢弃（`translate.go` 头注释记载；对照实验 9/9 验证）。
2. **conversation 的上下文窗口 ≈ 2M tokens**：浏览器内对同一
   conversation 连续增量累积 88 轮，1,923,198 tokens 时仍完整记得
   暗号，1.95M 起连续失败（`tools/webui_probe7_log.jsonl`）。
   另测：**单条消息上限 ≈ 15–16k tokens**，超限错误文案为
   `This request is too large to send. Shorten your message or selected text...`
   （编辑器话术，别与窗口上限混淆）。

### 生产策略：全量折叠（当前）

每轮 = **新 conversation + 全量 input**，历史由 `translateChatMessages`
折叠成 `[Previous Conversation History]` 文本块并进首条 system ——
单条消息客户端（如 Dashboard）的上下文由网关的会话历史缓存
（`internal/facade/session_chain.go`，24k 字符 / 20 条双预算 +
超限标注）自动注入。实测三轮暗号测试全通过。

### 原生续接协议（已逆向，待通道落地）

真实 Web 的多轮续接形状已完全逆向（playwright 抓包 + 页面内重放验证）：

```
第 2 轮起：conversationId = 上一轮 cdx1_*（不变）
           previousResponseId = 上一轮终态 payload.id（resp_* 形态）
           metadata.codex_listen_snapshot = 上一轮下发（含
             codex_session_id / transcript_cursor / last_turn_id）
           input = 仅本轮增量（编辑器状态 system + 本轮 user），零历史
           metadata.userId = user-* 形态（来自 access_token JWT 的
             chatgpt_user_id claim，网关已自动填充）
```

**在真实浏览器内重放同款请求 → 续接成功（答对暗号）；
Go tlsbridge 通道发同一请求 → 失忆** —— 上游把非浏览器级信任的
请求按"无状态会话"处理。落地件 `tools/browser_forward.js`
（8790 全请求浏览器代发）已可用但页面会话稳定性未打磨；
稳定后切换即可获得原生 2M 窗口 + 上游自动压缩。

### 对 Codex CLI 的配置建议

`~/.codex/config.toml`：

```toml
model_context_window = 16384   # 走网关时每次请求受单条 15–16k 限制，
                               # auto-compact 在 ~13k 触发刚好安全
```

（切换到 browser_forward 原生续接后，可把该值放大到接近 2M，
把历史与压缩完全交给上游。）

---

## 沙箱：必需的前置环节（已实测跑通）

> 完整调研与证据见 [`docs/Yjs依赖调研与沙箱方案.md`](docs/Yjs依赖调研与沙箱方案.md)。

Prism 的 AI 助手跑在一个**容器沙箱**里，而且它需要「项目工作区」才能工作。
完整链路是 **四步注入 + 一次等待**，全部已用真实凭据验证：

```
1. POST /api/backend/1/new                              申请沙箱
                                    → {url, token}
2. POST /api/projects/{id}/sandbox/resources-token       后端签发资源令牌（绑定项目，1h）
   body: {sandbox_session_id, sandbox_token}
                                    → {access_token, resources_base_url, expires_at}
3. POST <sandbox>/resources-token                        把令牌交给沙箱
   body: {token, resourceBaseUrl, projectId}
                                    → {"status":"success"}
4. POST /api/y                                           取 Y-Sweet 凭证
   body: {docId, requestContext}
                                    → {url(wss://…), baseUrl, authorization, token}
5. POST <sandbox>/token  (body = 第 4 步的整个对象)        原样转交
                                    → {"success":true,"message":"Token received"}
6. GET  <sandbox>/wait-for-sync?wait_ms=10000            等就绪 → status=synced
```

### 为什么不需要实现 Yjs

第 5 步之后是**沙箱自己**去连 Y-Sweet WebSocket 同步文档 ——
我们只是信使，一行 CRDT 代码都不用写。证据是状态机自证：

```jsonc
// 只申请沙箱，什么都不注入 → 永远停在这个状态
{"status":"syncing","tokens":{"hasResourceProjectId":false,
                              "hasCurrentYSweetToken":false,
                              "hasSyncedYSweetProvider":false,
                              "fileCredentialSource":"none"}}

// 交付 Y-Sweet 凭证后，1 秒内变成
{"status":"synced","tokens":{"hasResourceProjectId":true,
                             "hasCurrentYSweetToken":true,
                             "hasSyncedYSweetProvider":true,
                             "fileCredentialSource":"resources-token"}}
```

`hasSyncedYSweetProvider` 由 false 变 true 是它**自己同步完成**的标志。

> 顺带调研了 Go 生态的 Yjs 实现（全部实测 `go get`）：
> `github.com/reearth/ygo` v1.50.0 可用，只 import `crdt`+`sync` 时
> **只增加 1 个依赖**。其余候选要么模块路径写错、要么需要 cgo+Rust、要么已停更。
> **但当前不需要它** —— 沙箱自己完成同步，我们一行 CRDT 代码都不用写。

### 漏掉注入会怎样（症状极具误导性）

沙箱**不报任何错**，只是永远停在 `syncing`，最终表现为会话处理
**固定 122 秒**后 504，文案是 `Please submit prompt again.`
看起来像「上游挂了」或「容器冷启动慢」，实际上是它在等凭证。

看到「122 秒」这个特征值，直接去查 `wait-for-sync` 的 `tokens` 字段。

### 两个实战坑

1. **认证是双重的**：`X-Crixet-Sandbox-Token` + **Cookie**。
   只带前者会得到 `401` 且**响应体为空**——极难排查。
2. **沙箱 URL 不能当绝对 URL 直接发**：它形如
   `https://prism.openai.com/s/sandboxes/proxy/`，而我们的客户端是把 path
   **拼接**到 BaseURL 上的，传绝对 URL 会拼出 `/https://…` 这种畸形路径。
   正确做法是只取 path 再拼子路径，复用同一套连接池与 Cookie 注入。

### 性能实测

| 场景 | 延迟 |
|---|---|
| 首次（建项目 + 申请沙箱 + 四步注入 + 生成） | **14.2 s** |
| 同会话再次请求（项目/沙箱/同步全命中） | **5.6 s** |
| 沙箱工作区同步本身 | **2.6 – 3.3 s** |

同步状态按 **(账号, 项目)** 粒度缓存，失效点绑资源令牌的 `expires_at`
（1 小时）——令牌过期后沙箱读不到项目文件，与其等失败重试不如到点主动重同步。

会话复用键（`StickyKey`）= 显式 `X-Oaiprism-Session` 头 > 请求体 `user`
字段 > 首条消息指纹；它同时决定账号粘性、项目复用与（未来的）会话续接。
客户端用 `X-Oaiprism-Session` 显式指定时复用最稳定。

> 上述延迟数据为 **2026-09-17 在 tlsbridge 通道实测**（首次 14.2s /
> 复用 5.6s / 同步 2.6–3.3s）；经 browser_forward 实验通道时每请求多
> ~5–10s（每请求一次浏览器内 fetch + token 现签）。

### 相关配置

```yaml
facade:
  use_sandbox: true        # 关掉会退化成"申请了但不同步"，几乎必然失败
  sandbox_ttl: 30m
  sandbox_ready_wait: 90s  # 等的是"同步完成"，不是"容器启动"
```

## 安全要点

这几条是刻意实现的，改动时请留意：

1. **原样反代绝不回传上游的 `Set-Cookie`** —— 那是账号池账号的会话凭据，
   透传等于把账号送给调用方。
2. **原样反代丢弃调用方自带的 `Authorization` / `Cookie`** ——
   否则调用方可以用自己的凭据覆盖账号池，绕过所有调度与统计。
3. **`/admin/accounts` 不返回任何凭据内容**，只有元信息与运行态。
4. **凭据文件以 `0600` 写入**。
5. `facade.api_keys` 留空表示不校验——**仅限本机或内网**，暴露公网前务必设置。

---

## 运维

```bash
# 看账号是否还能用
./oaiprism.exe probe -config configs/config.yaml

# 看账号池运行态
curl http://127.0.0.1:8787/admin/accounts | jq

# 常用指标
curl -s http://127.0.0.1:8787/metrics | grep -E 'first_delta|facade_runs|poll_rounds'

# Dashboard（账号/会话调试/请求统计）
# 浏览器打开 http://127.0.0.1:8787/dashboard/
```

`readyz` 在没有可用账号时返回 503。这是刻意的：让"漏配凭据"
在部署阶段就暴露，而不是等到线上请求全 502。

---

## CI/CD 流水线

### 持续集成（`.github/workflows/ci.yml`）

触发：任意分支 push / PR / 手动 `workflow_dispatch`；
同一 ref 的新提交会取消上一次运行（`concurrency`）。

```
push / PR
   │
   ├─► job: go-check ─────────────────────────────────────────────┐
   │     1. checkout + setup-go（版本读 go.mod，缓存依赖）          │
   │     2. go vet ./...            静态检查（含格式化外的疑似问题） │
   │     3. go test ./...           单元测试（含模拟上游 e2e）      │
   │     4. go test -race ./...     并发竞态（账号池/SSE/热重载）   │
   │     5. go build                本机构建                        │
   │     6. 交叉编译                 linux/amd64 + linux/arm64      │
   │     7. 上传产物                 bin/oaiprism-linux-*（7 天）   │
   │                                                                │
   └─► job: web-check ────────────────────────────────────────────┤
         1. checkout + pnpm setup + node 22（缓存 pnpm store）     │
         2. pnpm install --frozen-lockfile（锁文件漂移即失败）     │
         3. npx tsc --noEmit            Dashboard 类型门禁          │
         4. pnpm build                  产出 web/dist（供网关伺服）│
                                                                   ▼
                                              绿色 = 可发布（进入下方部署）
```

**本地等价门禁**（提交前必须全绿，与 CI 完全一致）：

```bash
go vet ./... && go test ./... && go test -race ./...
cd web && npx tsc --noEmit && pnpm build
```

`-race` 不是可选项：本项目有账号池、粘性表、SSE 写出、热重载等大量
并发结构，历史上靠并发测试抓到过越界 panic。

### 持续交付（当前为半自动，无 CD 服务）

单二进制 + 静态 Dashboard 的形态让"发布"退化为**拷贝文件**，
故未引入 CD 平台，流程如下：

| 阶段 | 动作 | 产物 |
|---|---|---|
| ① 构建 | `make build-linux`（或 CI 产物下载） | `bin/oaiprism-linux-amd64` |
| ② 打包 | 二进制 + `config.example.yaml` + `web/dist` + `tools/sentinel_oracle.js` | 发布目录 / 压缩包 |
| ③ 部署 | 目标机放二进制与配置；`tools/start_bridge.ps1`（Windows）或等价 systemd unit（Linux） | 三件套运行中 |
| ④ 验证 | `GET /healthz` → Dashboard `/` → 一次真实推理 | 端到端通关 |
| ⑤ 回滚 | 保留上一版二进制，替换文件 + 重启（配置零迁移，SQLite 自动向前兼容） | 分钟级 |

> 桥与 oracle 是**有状态外部依赖**（浏览器登录态），因此部署单元是
> 「网关进程」；桥/oracle 属于运行环境，随机器初始化一次，日常发布不动。

---

## 目录结构

```
cmd/oaiprism/        命令行入口（serve / tlsbridge / probe / import / capture-summary）
internal/
  config/            配置加载与校验
  creds/             凭据建模、JWT 解析、自动续期（session + OAuth 双路径）
  httpc/             面向上游的调优 HTTP 客户端
  account/           账号池：调度策略、粘性、并发闸门、冷却、热重载
  prism/             上游协议客户端 + 宽容解析 + 前缀差分
  sse/               SSE 写出层（池化缓冲、手写 JSON 编码）
  facade/            兼容门面：OpenAI Chat / Responses / Anthropic Messages
  bridge/            Go TLS 桥（Chrome 系指纹传输 + Sentinel token 编排）
  rawproxy/          原样反代通道
  capture/           抓包录制与协议摘要
  middleware/        恢复、请求 ID、指标、鉴权、限流
  metrics/           零依赖 Prometheus 指标
  server/            组件装配与生命周期
web/                 Dashboard（React 19 + antd v6 + @ant-design/x），产出 web/dist
tools/
  start_bridge.ps1 / .cmd / stop_bridge.ps1 / check_bridge.cmd   一键启停与诊断
  sentinel_oracle.js  Sentinel token oracle（8791）
  browser_forward.js  浏览器全请求代发（实验件：原生增量续接通道）
  probe_*.py / test_*.py / verify_*.py    协议探测与真实链路验证脚本
  webui_probe*.js     playwright 真实浏览器协议考古（多轮续接/窗口实测）
  sentinel/           Sentinel 逆向资产（VM 解释器、解码脚本、文档）
configs/             config.example.yaml（入库）；config.yaml（本地，不入库）
docs/                协议校准报告、调用链、部署报告、对齐核对
.github/workflows/   CI（go-check + web-check）
```

---

## 测试

```bash
go test ./...            # 全量
go test -race ./...      # 竞态检测
go test -bench=. ./internal/...   # 基准
```

测试里包含一个**模拟上游**（`internal/server/e2e_test.go`），
它把"协议漂移"变成可复现的输入 —— 想验证"上游把 `messages` 换成 `outputs` 会怎样"，
在那里加个分支即可，不用真去抓包。

覆盖的用例：非流式/流式 chat、Anthropic 事件序列、Responses API、
项目复用与隔离、账号失效自动换号、无凭据错误提示、参数校验、
原样反代（含凭据注入与 `Set-Cookie` 剥离）、白名单、API Key 鉴权、
请求 ID 透传、运维端点、20 路并发流。

### 关于 `internal/prism` 的端点覆盖

该包实现了文档里列出的**全部**端点（projects / project-access /
conversation-history / llm start+status / sandbox render+render-status /
project-files upload / PATCH thumbnail）。其中一部分目前只被
原样反代通道使用，门面路径用不到 —— 这是有意的：
逆向出来的协议客户端应当完整，否则等你要用某个端点时还得回头补一遍。
详见 `internal/prism/client.go` 的 `Path*` 常量。
