# PrismOpenAIProxy 对齐核对报告

> 核对对象：`F:\Code\Active\PrismOpenAIProxy`（commit `164a402`，Node.js / 零依赖）
> 基准：OAIprism 用**真实凭据实测**出来的完整契约
> 方法：读全部源码 + 11 组真实上游往返实验。**所有结论都可复现，不是读代码猜的。**

---

## 一、结论

**协议层（生成部分）对齐得很好，甚至有两处比我的第一版更严谨；
但沙箱链路从头到尾没有实现，所以实际跑不通。**

三个决定性实验结果：

| 实验 | 结果 | 说明 |
|---|---|---|
| 用真实 Cookie 直接请求（沙箱变量留空） | `502` `Reconnecting to sandbox` **0.5 s** | 上游明确说"沙箱没准备好" |
| 我手工替它把沙箱四步注入做完，再启动 | `502` `Error while processing conversation (400)` **1.0 s**，**3/3 稳定复现** | 沙箱问题已解决，暴露出**第二个**问题 |
| 把 `model` 从 `sol` 换成 `gpt-5.6-sol` | ✅ **`200` + `PONG`，6.68 s** | 确认第二个问题是**模型名映射缺失** |

也就是说，它的失败是**两层叠加**的：

```
第一层：沙箱链路未实现 → sandbox_reconnecting（或 122 s 后 504）
第二层：模型名直通上游 → 只要传别名就 400
```

---

## 二、完全对齐的部分（14 项）

| # | 契约 | 它的实现 | 我的实测基准 |
|---|---|---|---|
| 1 | 路径是 `llm` 不是 `lim` | `prism-client.mjs:138` `/api/llm/response_with_tools_start` | ✅ 一致 |
| 2 | start 请求体 | `:141` `{input, previousResponseId, metadata, conversationId}` | ✅ 一致 |
| 3 | status 请求体 | `:175` `{request_id, turn_state}` | ✅ 一致 |
| 4 | `turn_state` 逐轮取最新 | `:175` 用 `envelope.turn_state` | ✅ 一致（关键正确性点） |
| 5 | 状态机 | `:147` started → `:179-182` pending/completed | ✅ 一致 |
| 6 | **嵌套错误识别** | `transform.mjs:137` 检查 `response.status === "error"` | ✅ 一致（做得比我的第一版对） |
| 7 | **倒序找最后一条 assistant** | `transform.mjs:153` 从后往前 + 跳过空文本 | ✅ 一致（细节比我原实现更稳） |
| 8 | **system 角色保留**在 input 数组 | `transform.mjs:94` `role: "system"` | ✅ 一致 |
| 9 | 兜底默认 system prompt | `transform.mjs:93-95` | ✅ 一致 |
| 10 | 块类型 `input_text`/`output_text` | `transform.mjs:48,55,63,109` | ✅ 一致 |
| 11 | 模型参数在 metadata 里 | `server.mjs:123-124` | ✅ 一致（曾是我的错判点） |
| 12 | `frontend_origin` | `server.mjs:125` | ✅ 一致 |
| 13 | `origin`/`referer`/`cookie` 头 | `prism-client.mjs:89-98` | ✅ 一致 |
| 14 | 图像转 `input_image` | `transform.mjs:56-65` | ✅ 一致，且**比我多带 `detail`** |

### 它比我做得好的三处（值得反向借鉴）

1. **`tool` role 的处理更清晰**（`transform.mjs:34-44`）：折成 `role:"user"` 并标注
   `[<toolName> result]`。我用的是把 tool_calls 塞进文本，可读性不如它。
2. **`userId` 注入 metadata**（`server.mjs:122`，来自 `PRISM_USER_ID`）：我没有这个能力。
3. **模型列表支持 label**（`prism-client.mjs:12-46`）：`/v1/models` 能返回展示名，
   我只返回 id。

---

## 三、未对齐的部分

### 3.1 沙箱链路：**8 项全部缺失**（最严重）

它有 `PRISM_SANDBOX_URL` / `PRISM_SANDBOX_TOKEN`（`.env.example`、`prism-client.mjs:67-68`），
但**只是把用户手工填的值转进 metadata**（`server.mjs:126-127`），
后续四步注入一个都没做：

| # | 缺失环节 | 端点 | 后果 |
|---|---|---|---|
| 1 | 申请沙箱 | `POST /api/backend/1/new` | 用户必须自己去某处搞到 url+token |
| 2 | 签发资源令牌 | `POST /api/projects/{id}/sandbox/resources-token` | 沙箱永远拿不到项目资源 |
| 3 | 注入资源令牌 | `POST <sandbox>/resources-token` | 同上 |
| 4 | 取 Y-Sweet 凭证 | `POST /api/y` | `hasCurrentYSweetToken` 永远 false |
| 5 | 交付凭证 | `POST <sandbox>/token` | `hasSyncedYSweetProvider` 永远 false |
| 6 | 等就绪 | `GET <sandbox>/wait-for-sync` | 没有就绪判定，发出去就撞 |
| 7 | 自动建项目 | `POST /api/projects` | `PRISM_PROJECT_ID` 必须手工填 |
| 8 | 主动停止 | `POST /api/llm/response_with_tools_stop` | 客户端断开后上游仍跑完扣额度 |

**实测证据**：我只给了 `PRISM_PROJECT_ID` + `PRISM_SANDBOX_URL` + `PRISM_SANDBOX_TOKEN`
（这三个是它要求的全部输入），沙箱**已经是我预先同步好的**，请求才走通。
也就是说 —— **它能跑的前提是"有人先替它把沙箱同步好"**，而这件事它不做。

### 3.2 模型别名映射缺失（本轮新发现）

```javascript
// server.mjs:123
model: body.model || config.model,
```

请求里的 `model` **原样转发给上游**。但对外暴露的是别名（`sol` / `gpt-5-high` 之类），
上游只认真实模型名 `gpt-5.6-sol`。实测：

```
POST /v1/chat/completions  {"model":"sol"}
  → 502 {"message":"Error while processing conversation (400 Bad Request)…"}

POST /v1/chat/completions  {"model":"gpt-5.6-sol"}
  → 200 {"content":"PONG"}                      ← 一次就通
```

而 `PRISM_MODEL` 默认值恰好是 `gpt-5.6-sol`，所以**如果客户端不传 model，反而能工作**；
一旦传了别名就 400 —— 这种"看情况才坏"的 bug 最难排查。

我的实现里有 `facade.models` 映射表（`sol` → `gpt-5.6-sol` + effort），
对外友好名与上游真名解耦，上游换模型名只改 YAML。

### 3.3 请求头缺 User-Agent

`headersFor()`（`prism-client.mjs:89-98`）只设 accept/content-type/origin/referer/cookie。
**实测 Node 原生 fetch 默认发 `user-agent: node`**：

```json
{"user-agent": "node", "accept-language": "*", ...}
```

而实测确认 Cloudflare 会对"不像浏览器的请求"做拦截（曾把 503 误判成上游维护）。
目前它没被拦（我这次往返成功），但 `user-agent: node` 是个明确的风控靶子。
我的实现会伪装完整的 Chrome UA + `sec-fetch-*` 头。

### 3.4 `/healthz` 被 API Key 拦截

```javascript
// server.mjs:229-231
if (!authorized(req)) { return json(res, 401, ...); }   // ← 在路由匹配之前
...
if (req.method === "GET" && url.pathname === "/healthz") { ... }   // ← 永远不会走到
```

实测（`PROXY_API_KEY=sk-test-key`）：

```
GET /healthz                    → 401 {"error":{"message":"Invalid proxy API key"}}
GET /healthz  (带 key)          → 200
```

**k8s / Nginx / Docker 的存活探针会一直拿到 401，然后反复重启容器。**
探针应豁免于鉴权。

### 3.5 其它差异

| 项 | 它 | OAIprism | 影响 |
|---|---|---|---|
| 轮询节奏 | 固定 3 s（`prism-client.mjs:162`，且 **sleep 在前**） | 自适应退避 + 立即首轮 | 首字延迟至少多 3 s |
| 真流式 | ❌ 轮询结束后一次性发 2 帧（`server.mjs:148-172`） | 前缀差分还原增量 | 它 README 已承认 |
| `usage` | 硬编码全 0（`server.mjs:143`） | 从 `payload.usage` 提取 | 计费/统计不可用 |
| 项目/沙箱缓存 | ❌ 无 | 项目池 + 沙箱缓存（(账号,项目) 粒度） | 每次全量重走 |
| 账号池 | ❌ 单账号单 Cookie | 多账号 + 粘性 + 并发闸门 + 冷却 | 无法横向扩容 |
| 并发保护 | ❌ 无全局限流 | 令牌桶 | 容易被自己打爆上游 |
| 可观测性 | 只有 `console.log` | Prometheus 文本指标 + `/admin/*` | 无法上监控 |
| 上游 401 映射 | 转成 **401** 给客户端（`server.mjs:212-213`） | 转成 **502** | 语义分歧：上游凭据失效是**网关**的问题，返回 401 会让客户端以为自己的 key 错了 |
| 客户端断开 | 只 abort 自己的 fetch（`prism-client.mjs:108-109`） | 同时调上游 `stop` | 断开后上游仍跑完，白耗额度 |
| 会话延续 | 靠客户端显式传 `previous_response_id` | 会话键自动复用项目 | 多轮场景需客户端配合 |

---

## 四、如果要让它对齐（按性价比排序）

**必做（不然跑不通）**

1. **补模型映射**：加一张 `对外名 → {model, reasoning_effort}` 表，
   `metadataFor()` 里查表而不是直通。约 20 行。
2. **补沙箱四步**：申请 → 签发资源令牌 → 注入 → 交付凭证 → 等就绪。
   参考 `tools/verify_sandbox_flow.py` 里的可运行实现（约 80 行 Python，转成 JS 更短）。
3. **补自动建项目**：`POST /api/projects {project_uuid, title}`。

**应该做（生产可用性）**

4. `/healthz` 豁免鉴权（否则容器起不来）。
5. 加 `User-Agent`（伪装 Chrome），降低 CF 风控风险。
6. 客户端断开时调 `stop` 端点。
7. 沙箱/项目的缓存（否则每个请求都要重新同步，代价 ≥ 3 秒）。

**可选**

8. 上游 401 改映射为 502。
9. `usage` 从上游读取。
10. 轮询改成"先 poll 再 sleep"，或加自适应退避。

---

## 四之二、核对之后的落地（本轮实际改动）

报告写完不算完 —— 声明"已借鉴"就必须真的改代码。本轮改动如下，
每一项都**做了回退验证**（故意把修复撤掉，确认对应测试变红），
因为"测试通过"不等于"测试有效"。

### 1. 修掉自家 bug：图像 URL 被静默丢弃（严重）

```go
// 修复前 translate.go
case "image_url", "input_image", "image":
    text, _ := b["text"].(string)          // ← URL 在 b["image_url"]["url"]，这里恒空
    out = append(out, prism.InputContent{Type: "input_image", Text: text})
```

`InputContent` 结构上**根本没有 URL 字段**，所以发上去的是一个空 `input_image`：
模型看不见图，客户端和服务端都不报错。而测试只断言了 `Type`，所以一直没暴露。

改动：
- `prism.InputContent` 增加 `ImageURL` / `Detail`（`types.go:187`）
- `translate.go` 新增 `imageURLAndDetail()`，覆盖三种真实写法：
  OpenAI 标准（`image_url` 是对象）、简化写法（`image_url` 是字符串）、
  Responses 风格（`detail` 在块这一层）
- 取不到 URL 时退化成文本而不是丢弃
- 测试补到**内容级**断言（不再只断言 `Type`），新增 `TestToInputContent_ImageVariants`

> 回退验证：把 URL 提取短路掉 → 3 个测试立刻变红。

### 2. 补齐 `user` 字段透传（中等）

对照时发现它把 `userId` 注入 metadata，而我们**请求结构里早就有 `user`
的 json tag，却没有任何代码把它送下去** —— 解析了、丢掉了，同样不报错。

改动（链路共 5 处，缺一就是静默丢弃）：
- `facade.RunRequest` 增 `UserID`，`runner.go:325` 传给 `prism.StartRequest`
- `prism.StartRequest` 增 `UserID`，`buildStartPayload` 写进 `metadata`
- `chat.go` / `responses.go` 分别从 `req.User` 取值
- Anthropic 侧新增 `anthropicUserID()`，从 `metadata.user_id` 取
  （此前它只用于会话粘性，没传上游；现在两处共用同一个 helper）

字段名走 `facade.schema.field_user_id`（默认 `userId`，设 `-` 可关闭），
遵守"上游字段名不硬编码"的项目硬性规则 —— 这个值同样属推断值，待真实报文验证。

> 回退验证：删掉 `UserID: req.User` → 端到端测试
> `TestE2E_UserReachesUpstream` 立刻变红（`metadata.userId = <nil>`）。
> 单元测试测不到"中间一层忘了传"，所以必须补集成级断言。

### 3. tool 结果标注更清楚（轻微）

`tool` 角色折成 user 时，标注从 `[name]` 改成 `[name result]\n正文`：
只写工具名会让模型分不清"这是工具回传的结果"还是"用户提到的一个名字"。

### 4. 已核对确认**本来就对齐**的两项

此前报告声称"已反向借鉴"的另两处，逐行核对确认已落地，且比它更完善：

| 项 | 位置 | 状态 |
|---|---|---|
| 嵌套 `response.status=="error"` 识别 | `prism/client.go:773` | 已有，且注释点明"HTTP 200 + error 是本协议最易踩的坑" |
| 倒序找最后一条**有内容的** assistant | `prism/client.go:882` | 已有，且额外跳过非 message / 非 assistant / 空文本，并有"无 role"兜底 |

### 5. 仍未补的一项

**模型 label**（它在 metadata 里带模型显示名）—— 我们的 `facade.models`
映射表已有别名→真名+effort，但没带 label。价值有限，记为待办而非本轮改动。

---

## 四之三、把"它有我没有"的全部补齐（第二轮）

第一轮补的是**它做得比我好的地方**；这一轮把剩下"它有我没有"的能力全部对齐。
逐项以它的源码为基准（`server.mjs` / `prism-client.mjs` / `lib/transform.mjs`）。

| # | 它的能力 | 位置 | 我补齐的做法 |
|---|---|---|---|
| 1 | 模型 label（`/v1/models` 返回 `name`） | `prism-client.mjs:12-46`、`server.mjs:202` | `config.ModelMapping` 加 `label`；`ModelInfo` 加 `name`（无 label 时不发该字段） |
| 2 | 客户端 `metadata` 过滤后透传 | `server.mjs:101-118` | 新增 `clientMetadata()` + `metadataReservedKeys`（12 个保留键），三端点接线 |
| 3 | `reasoning_effort` 三级回落 | `server.mjs:124` | 新增 `metadataEffort()`：顶层字段 > `metadata.reasoning_effort` > 映射表 |
| 4 | `conversation_id` 双向透传 | `server.mjs:177,190` | 新增 `conversationIDFrom()` + `setConversationHeader()`；`RunRequest.ConversationID` 此前是**死字段** |
| 5 | 图像 `detail` 缺省 `"auto"` | `transform.mjs:61,65` | `imageURLAndDetail()` 返回值缺省回填 `"auto"` |
| 6 | 无 `/v1` 前缀的路由别名 | `server.mjs:237,240` | 注册 `/chat/completions`、`/models` |
| 7 | CORS + OPTIONS 预检 | `server.mjs:52-60,225` | 新增 `middleware.CORS`，`server.cors_origin` 配置，空即零成本透传 |

### 本轮新发现并修掉的两个真 bug

**Bug A：`RunRequest.ConversationID` 是死字段。**
结构体里有、三个 handler 里一个都没赋值 —— 客户端即便拿到会话 ID 也续不上。
这是"字段存在但没人写"的静默失效，编译器不会报错。

**Bug B：`conversation_id` 被当未知字段透传进请求体顶层。**
回退验证时从失败输出里抓到的：

```
上游收到: {..., "conversation_id":"conv-back", "conversationId":"conv-back"}
```

客户端按 OpenAI 习惯传 snake_case，被 `passthroughFields` 当成"上游新参数"
原样塞进顶层。上游只认 camelCase 的 `conversationId`，多一个蛇形键属于污染。
修法：把 `conversation_id` / `conversationId` 加进 `knownFields`（它已被显式消费）。

### 一个"不加就白做"的细节：`Access-Control-Expose-Headers`

CORS 里光有 `Allow-Headers` 不够 —— `x-prism-conversation-id` 不在 CORS
安全列表里，浏览器默认不许 JS 读自定义响应头。少了 `Expose-Headers`，
跨域客户端拿不到会话 ID，多轮续写会**静默退化成每次都是新会话**。
已一并写入 `middleware.CORS` 并加断言。

### 保持我方实现的分歧（不跟）

| 项 | 它 | 我 | 理由 |
|---|---|---|---|
| assistant 历史块类型 | 一律 `input_text`（`transform.mjs:76`） | `output_text` | 我方依据真实前端源码注释"助手历史用 output_text，传错不报错但模型看不见"。它的实测只跑了单轮 user 消息，未验证多轮。标注为**待确认分歧** |

### 验证

- 门禁：`gofmt` 空、`go build`、`go vet`、`go test -count=1 ./...`、`go test -race` 全绿
- 新增 11 个测试（facade 6 + middleware 3 + server E2E 3，含一个回归断言）
- 关键项做回退验证：撤掉 metadata 透传与 conversationID 赋值 → 对应 E2E 立刻变红

---

## 五、一句话总结

> 它把我**协议层**做对了（甚至有 3 处比我的第一版更严谨，我已反向借鉴），
> 但**没做沙箱链路和模型映射**这两件"不报错、只是永远不成"的事。
>
> 换句话说：它是"**协议的实现者**"，而 OAIprism 是"**整条链路的实现者**"。
> 它的 726 行里，协议部分对齐度很高；缺的 8 项沙箱环节是"没有它就跑不通"的硬缺口。

---

## 附：复现命令

```bash
# 1) 它缺沙箱 → 看 sandbox_reconnecting
PORT=18811 PROXY_API_KEY=sk-test-key node server.mjs
curl -H 'Authorization: Bearer sk-test-key' -H 'content-type: application/json' \
     -d '{"model":"sol","messages":[{"role":"user","content":"hi"}]}' \
     http://127.0.0.1:18811/v1/chat/completions
# → 502 Reconnecting to sandbox

# 2) 手工替它同步好沙箱（OAIprism 的脚本）
python tools/prepare_sandbox.py          # 打印 PRISM_PROJECT_ID / SANDBOX_URL / SANDBOX_TOKEN
# 用这三个变量重启它

# 3) 仍失败 → 暴露模型映射缺失
curl ... -d '{"model":"sol", ...}'        # → 502 400 Bad Request
curl ... -d '{"model":"gpt-5.6-sol", ...}' # → 200 PONG  ← 一次就通

# 4) healthz 被鉴权拦
curl http://127.0.0.1:18811/healthz                  # → 401
curl -H 'Authorization: Bearer sk-test-key' .../healthz  # → 200
```
