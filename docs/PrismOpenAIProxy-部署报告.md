# PrismOpenAIProxy 部署报告

部署位置：`F:\Code\Active\PrismOpenAIProxy`（与 OAIprism 同级）
来源：`https://github.com/openaeon/PrismOpenAIProxy`（commit `164a402`，分支默认）
部署时间：2026-09-17

---

## 一、环境依赖（结论）

### 硬性要求：只有一个 —— Node.js ≥ 18.18

| 项 | 要求 | 本机情况 |
|---|---|---|
| **Node.js** | **≥ 18.18**（`package.json` 的 `engines` 声明） | ✅ 22.22.2（托管）/ 24.13.0（系统） |
| npm 包依赖 | **零**（`dependencies` 与 `devDependencies` 都不存在） | ✅ `npm install` 只审计 1 个包（它自己） |
| 构建步骤 | **无**（纯 ESM，`node` 直接跑） | ✅ |
| 数据库 / 缓存 / 外部服务 | **无** | ✅ |
| 操作系统 | 不限（只用 Node 内置模块） | ✅ Windows 11 |

实测确认可用的断言（`npm install` 后）：

```
$ npm install
up to date, audited 1 package in 1s
found 0 vulnerabilities

$ ls node_modules
（不存在 —— 说明确实零依赖）
```

### Node 18.18 这个下限是怎么来的

项目用到这些**原生能力**，都不用装包：

| 能力 | 首次可用版本 | 用途 |
|---|---|---|
| 全局 `fetch` | 18.0（默认开启） | 调用 Prism |
| `AbortController` | 15.0 | 请求超时/取消 |
| `node:test`（`node --test`） | 18.0 | 跑测试 |
| `crypto.randomUUID` | 14.17 | 生成 chatcmpl id |
| ESM（`"type": "module"`） | 12+ | 模块体系 |
| `String.replaceAll` | 15.0 | id 去横线 |

所以真正的下限是 18.0，作者写 18.18 是取了当时那个 LTS 版本。

### 功能上必需的东西：一个有效的 Prism 会话 Cookie

**这是唯一"没它就跑不起来"的外部输入。**

- 变量名：`PRISM_COOKIE`
- 值：`prism.openai.com` 登录后的整串 Cookie（至少要含 `__Secure-next-auth.session-token`）
- 前提：账号得有 **Plus / Pro 订阅**（Prism 只对这两个档开放）
- 安全：`server.mjs` 只从环境读，**不接受调用方传入的 Cookie**；`.env` 已在 `.gitignore` 里

没有它时服务**仍能启动**，只是 `/healthz` 会返回 `prism_configured:false`，
聊天请求会失败并给出明确错误。

---

## 二、部署步骤（已执行）

```bash
cd F:/Code/Active
git clone --depth 1 https://github.com/openaeon/PrismOpenAIProxy.git PrismOpenAIProxy
cd PrismOpenAIProxy
npm install            # 零依赖，秒过
npm test               # 4/4 通过
cp .env.example .env   # 然后填 PRISM_COOKIE
npm start              # 默认 127.0.0.1:8787
```

当前状态：**已启动并在运行**，监听 `127.0.0.1:8787`。

---

## 三、运行验证结果

| 检查项 | 结果 |
|---|---|
| `npm test` | ✅ 4/4 通过（transform 2 项 + client 2 项） |
| `GET /healthz` | ✅ `{"ok":true,"prism_configured":false,"project_configured":false}` |
| `GET /v1/models` | ✅ 返回 `gpt-5.6-sol` |
| `GET /models`（别名） | ✅ 200 |
| 未知路由 | ✅ 404 + OpenAI 风格错误体 |
| 空 `messages` | ✅ 400 `messages must be a non-empty array` |
| 未配 Cookie 时发聊天 | ✅ 502 `PRISM_COOKIE is not configured` |
| `PROXY_API_KEY` 鉴权 | ✅ 不带 key → 401；带 key → 200 |
| **真实上游往返**（假 Cookie） | ✅ 打到了 `prism.openai.com`，拿到上游真实错误 `User not found`，并正确映射为 502 |

最后一条最关键：说明 **TLS / HTTP2 / 请求头 / 请求体 / 响应解析整条链路都是通的**，
只差一个有效 Cookie。

---

## 四、它的协议实现与实测结论是否一致

**完全一致。** 这份实现反过来印证了我方（OAIprism）上轮通过探测得出的结论：

| 项 | 本项目 | 我方实测 |
|---|---|---|
| 路径 | `/api/llm/response_with_tools_start` | 同 ✅（`lim` 是笔误） |
| 请求体 | `{input, previousResponseId, metadata, conversationId}` | 同 ✅ |
| 句柄字段 | `request_id` | 同 ✅ |
| 轮询体 | `{request_id, turn_state}` | 同 ✅ |
| `turn_state` 语义 | 逐轮取最新值回传（`envelope.turn_state`） | 同 ✅ |
| 状态机 | `started` / `pending` / `completed` | 同 ✅ |
| 失败表达 | `response.status === "error"` → 抛错 | 同 ✅ |
| 答案位置 | `output` 里**倒序找最后一条 assistant 消息** | 同 ✅（我取最后一条 item） |
| 默认模型 | `gpt-5.6-sol` | 同 ✅ |
| 模型列表来源 | Statsig 动态配置 `prism_codex_models` | 同 ✅ |
| 是否 token 流式 | **否**（README 明确写了） | 同 ✅（我标为"待确认"，这里得到确认） |

**值得吸收的两个细节**：

1. 它把 system 提示作为 `role:"system"` 留在 `input` 数组里，而不是折成 user 消息。
   同一条 `input` 数组里允许 `system` 角色——比我方的处理更贴近原始形状。
2. 取答案时是**倒序找最后一条 `role==="assistant"` 的消息**，并跳过空文本；
   比我方"只看最后一个 item"更能容忍尾部夹带非消息条目。

**我方的差异（不是对错，是覆盖度）**：

- 它**没有**用 `response_with_tools_stop`；我方在客户端断开/超时时会调它，避免上游白跑完扣额度。
- 它只做 Chat Completions；我方还做 Responses API 与 Anthropic Messages。
- 它**不做多账号池**（单 Cookie）；我方有多账号、粘性、冷却。

---

## 五、部署注意事项（踩过的坑）

### 1. 端口与 OAIprism 冲突

本项目默认 `8787`，我方 OAIprism 也默认 `8787`。**两者不能同时用默认端口。**

改其中一个的 `.env`：
```
PORT=8790
```

### 2. `/healthz` 也会被 API Key 拦住

`server.mjs` 里 `authorized(req)` 的检查在路由分发**之前**：

```js
if (!authorized(req)) { return json(res, 401, ...); }   // ← 先拦
const url = new URL(...);
if (url.pathname === "/healthz") { ... }                 // ← 后匹配
```

一旦设了 `PROXY_API_KEY`，**不带 key 的健康检查会拿到 401**。
如果要用 Nginx / k8s 做存活探针，要么别设 API Key，要么让探针带上
`Authorization: Bearer <key>`，要么给它前面套一层不打 auth 的反代。

### 3. 轮询间隔默认 3 秒

`PRISM_POLL_INTERVAL_MS=3000`。上游不是 token 流式的，所以 `stream:true` 也只是
**在轮询结束后一次性发两个 SSE 事件**，不是逐字流。想要更低的感知延迟可以调小这个值，
但要自己盯上游的限流。

### 4. 零依赖 → 没有 `npm audit` 的收益，但也没有供应链风险

好处是不用管 CVE、不用锁版本、`npm ci` 秒过。代价是没有依赖 = 协议变更时
要自己改代码（字段名没做成配置，是硬编码的）。

---

## 六、与我方 OAIprism 的关系

两个项目**可以并存，定位不同**：

| | PrismOpenAIProxy | OAIprism |
|---|---|---|
| 语言/运行时 | Node.js（零依赖） | Go 单二进制 |
| 定位 | 极简适配器，单 Cookie | 生产级网关，多账号池 |
| 端点 | Chat Completions | + Responses API + Anthropic Messages + 原样反代 |
| 协议字段 | 硬编码 | 配置化（改 YAML 不用重编译） |
| 账号 | 单账号 | 多账号 + 粘性 + 冷却 + 快慢失败区分 |
| 上游停止 | 未实现 | 已实现（断开/超时/写失败三处） |
| 可观测 | 仅 `/healthz` | Prometheus 指标 + 管理端点 |
| 适合 | 本机自用、快速验证协议 | 长期跑、对外提供服务 |

**建议用它来先验证 Cookie 是否有效**（代码量小、路径干净、出错信息直接），
确认通了之后再切到 OAIprism 跑生产。
