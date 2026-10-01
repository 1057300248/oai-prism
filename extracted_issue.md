# [Research] Prism 多项目取长补短，整合为 Codex Responses API Bridge

## 目标仓库

本议题面向 `ranxi2001/sub2api`，讨论将 `prism.openai.com` 的 agent 能力接入 Sub2API / Codex 的实现路径。应用源码后续应在本仓库的生产分支或独立 worktree 中按项目规则实现。

## 目标

调研如何把 `prism.openai.com` 的 agent 能力转换成 Codex 可直接使用的 OpenAI Responses API，并把现有多个项目的成熟部分整合成一套可维护的协议桥接层。

这里的目标不是选定某一个现成项目直接部署，而是提炼各项目的实现经验，形成一个统一的 Prism → Codex API 方案。

## 当前结论

Prism 的网页后端使用 `response_with_tools_start` / `response_with_tools_status` 这类 start + poll 交互；Codex 自定义 Provider 则要求桥接器对外提供 Responses API。仅设置 `base_url` 不能解决以下差异：

- Codex 的 `/v1/responses` 请求需要转换成 Prism 的私有请求结构。
- Prism 上游没有可依赖的标准 `tools` 通道，工具定义需要通过受控提示协议传递，再还原为 Codex 的工具事件。
- Prism 的轮询状态、`turn_state`、sandbox 和项目上下文必须独立维护。
- Codex 的 `function_call`、`custom_tool_call`、`additional_tools`、namespace、工具结果回灌和取消语义需要保持一致。
- start 请求超时或结果不明时不能自动重放，否则可能重复提交同一个任务。
- Prism 没有可依赖的逐 token 文本流。桥接器只能转发已观察到的进度、工具事件和最终结果，不能伪造 token 流或 token usage。

Codex 的 Gateway 兼容要求也集中在 Responses API endpoint、streaming、continuation、tool calls、authentication、routing 和错误语义上：[Gateway compatibility requirements](https://learn.chatgpt.com/docs/enterprise/gateway-compatibility)。

## 多项目取长补短

| 项目 | 适合吸收的部分 | 需要保留的边界 |
|---|---|---|
| [jin-wind/prism2api](https://github.com/jin-wind/prism2api) | Responses 事件转换、`function_call` / `custom_tool_call`、namespace、工具结果回灌、`previous_response_id`、取消、不可重放状态机 | 仍依赖未公开的 Prism 网页协议；图片、Web Search、远端 MCP 和部分高级 Responses 能力明确未支持 |
| [Seventy73-oss/prism-proxy](https://github.com/Seventy73-oss/prism-proxy) | Node.js 代理结构、Chat/Responses/Anthropic 入口、mock server、离线合同测试、工具提示协议 | Prism 终态文本不是原生 token stream；工具调用仍是提示词级 shim |
| [lvzhentao/chatgpt-prism2api](https://github.com/lvzhentao/chatgpt-prism2api) | Go 服务、账号登录/续期、账号池、项目和 sandbox 隔离、附件、用量与管理台 | 工具调用主要依靠提示词仿真；账号池、并发和容量结论需要重新验证 |
| [Danchuna/prismctl2api](https://github.com/Danchuna/prismctl2api) | 本地工具桥、工具声明缓存、调试控制台、模型目录、`-local-tools` 工作流 | 本机命令执行权限必须留在 Codex 或本机 Agent，网关不能直接执行模型生成的 shell |
| [zyxzjyzjj/cpa-gptlatex-plugin](https://github.com/zyxzjyzjj/cpa-gptlatex-plugin) | `additional_tools`、namespace、Codex `custom` 工具识别、稳定工具 ID 和结果回传 | 它是 PluginBridge 形态，不能直接替代独立 API 网关 |
| [Zhao73/free-astra](https://github.com/Zhao73/free-astra) | Codex 前门、模型别名、清理过长的 Codex 指令、模型能力隔离 | 使用非公开上游接口；非原生流式、usage 估算和模型 fallback 不能作为生产契约 |
| [Robinfxa/prism2api](https://github.com/Robinfxa/prism2api) | fixed-context、pending journal、单并发、结果不明时禁止重放、明确的 unsupported 边界 | Direct HTTP 路径更适合固定单用户会话，不能直接等同于多账号生产网关 |
| [openaeon/PrismOpenAIProxy](https://github.com/openaeon/PrismOpenAIProxy) | 最小化 start/status 客户端、代理认证、可读的基础测试 | 功能范围较小，只适合作为基础回退实现 |
| [FaFengFei1961/prism-ai-gateway](https://github.com/FaFengFei1961/prism-ai-gateway) | 多账号调度、Responses 事件、reasoning summary、控制台和日志指标 | 自动生成补丁或写文件的实现需要严格收敛到 Codex 工具权限模型，不能默认替用户落盘 |
| [jiusanzhou/prism](https://github.com/jiusanzhou/prism) | Rust 路由引擎、Provider / Account / Model / Variant 分层调度、fallback、重试、熔断和 OpenAI-compatible 网关 | 它是通用 LLM 路由层，不负责 Prism 网页协议、ChatGPT OAuth、sandbox 生命周期或 Codex 工具闭环；只能作为桥接器上层的路由与容错参考 |

## 建议的目标架构

```text
Codex CLI / Desktop
        │
        │  OpenAI Responses API
        ▼
Prism-Codex Bridge（默认只监听 127.0.0.1）
  ├─ Responses 请求校验与归一化
  ├─ input / instructions / history 管理
  ├─ function、custom、namespace、additional_tools 映射
  ├─ turn / sandbox / project 生命周期
  ├─ pending journal 与幂等保护
  ├─ 账号池、session affinity、冷却和故障分类
  ├─ SSE 事件转换与真实 usage 标记
  └─ 本地审计与敏感信息脱敏
        │
        │  Prism start/status 协议
        ▼
prism.openai.com
```

Codex 的本地配置可以采用：

```toml
model = "prism-sol"
model_provider = "prism"

[model_providers.prism]
name = "Local Prism Bridge"
base_url = "http://127.0.0.1:8765/v1"
env_key = "PRISM_BRIDGE_API_KEY"
wire_api = "responses"
supports_websockets = false
```

## 第一阶段必须明确的协议

### 请求

- 支持字符串 `input` 和 Responses item 数组。
- 支持 `instructions`、普通文本 history、`function_call` 和 `function_call_output`。
- 支持 Codex 的 `additional_tools` 和 namespace。
- 明确拒绝或记录未实现的图片、文件、hosted web search、远端 MCP、background mode 和 JSON Schema 最终输出。
- 对工具名、工具类型、JSON Schema、`call_id` 和 `tool_choice` 做严格校验。

### 上游适配

- Prism start 请求只提交一次。
- status 查询可以在安全边界内重试，并持续保存最新 `turn_state`。
- start 超时、连接断开或状态不明时写入 pending journal，禁止自动重放。
- 只有明确的 terminal success / terminal error 才能清理 pending 状态。
- sandbox 过期只能在确认过期信号后重建，不能把普通 5xx、401 或 timeout 猜成过期。

### 工具闭环

- 模型只能请求调用客户端声明过的工具。
- 网关只解析、校验和转发工具调用，不执行本地 shell、补丁、MCP 或文件写入。
- Codex 执行工具后，网关将结果按原始 `call_id` 回灌给 Prism。
- `custom_tool_call` 需要保留 free-form input；普通 function tool 需要保留 JSON arguments。
- 解析失败、未知工具名、重复 `call_id` 和不匹配结果必须失败关闭，不能把错误内容继续当作模型文本。

### 流式和用量

- 可以发送 `response.created`、工具事件、reasoning summary 和 heartbeat。
- Prism 只返回终态文本时，不能伪造逐 token `output_text.delta`。
- 没有官方 usage 时返回 `usage: null` 或显式 `estimated`，不能冒充真实计量。
- 对取消、上游失败、超时和不确定结局使用不同的 `response.failed` / 状态码 / 日志分类。

## 分阶段实施

### P0：协议合同和离线 fixture

- 固定 Responses 请求/事件样本。
- 固定 Prism start/status mock。
- 覆盖重复提交保护、`turn_state` 更新、终态识别、工具 schema 和 `call_id`。
- 所有 fixture 脱敏，不保存 Cookie、HAR、access token、sandbox token 或项目内容。

### P1：单账号文本闭环

- 完成单账号登录态读取、项目/sandbox 初始化、文本请求和终态响应。
- 完成 Codex CLI smoke test。
- 只监听回环地址，使用独立 bridge key。

### P2：Codex 工具闭环

- 完成 function、custom、namespace、additional_tools。
- 验证两轮以上工具调用和结果回灌。
- 验证取消、超时、上游失败和 pending journal 恢复。
- 确认本地工具始终由 Codex 权限系统执行。

### P3：账号池和资源能力

- 每个账号独立维护 Cookie/session/project/sandbox。
- 加入 session affinity、冷却、失败分类、账号切换和 token 续期。
- 再评估图片、附件和更长历史；未验证能力继续明确拒绝。

### P4：兼容性和运维

- Chat/Responses 入口的兼容性测试。
- 模型目录和 reasoning effort 的动态同步。
- 指标、脱敏日志、备份和升级流程。
- 记录 Prism 网页协议变更后的重新抓取和回归流程。

## 验收标准

- Codex 能通过自定义 Provider 完成普通文本 Responses 请求。
- Codex 能完成至少一轮 function tool 和一轮 custom tool 的本地执行闭环。
- 工具结果能按原始 `call_id` 回灌，未知工具和非法参数被拒绝。
- start 请求发生不确定失败后，重试不会产生第二次上游提交。
- status 查询能正确保留并更新 `turn_state`。
- 上游终态文本不会被伪装成逐 token 流。
- usage 缺失时不会返回伪造的官方 token 数。
- 日志和错误响应不包含 Cookie、access token、HAR、sandbox token、完整提示词或完整工具参数。
- Prism 协议变更时，mock fixture、协议版本和兼容性测试可以定位受影响的阶段。

## 已完成的本地复核

2026-10-01 对公开参考实现做了离线复核：

- `Seventy73-oss/prism-proxy`：60 项离线代理测试、8 项 mock 测试和 9 项认证测试通过。
- `openaeon/PrismOpenAIProxy`：4 项 Node.js 测试通过。
- 本仓库冻结的浏览器适配器：74 项 Python 测试通过。
- 这些结果只证明离线/模拟合同，不证明 Prism 当前线上协议、长期稳定性、账号池容量或生产可用性。
- `jin-wind/prism2api` 的 README 记录 v0.3.3 单元测试为 112 passed；本次没有把该项目的实时账号测试当作验证证据。

## 风险和待决策问题

1. Prism 私有 start/status 协议可能随网页发布变更，需要协议版本和 fixture 更新机制。
2. Prism 的模型清单、套餐权限和 reasoning effort 不能假设长期固定。
3. OAuth token、Cookie、sandbox token 和项目上下文的生命周期不同，需要分别管理。
4. 远端 Prism agent 可能拥有自己的 sandbox 工具；桥接器必须持续阻止它越过本地工具边界。
5. 账号池、并发、附件和大上下文能力需要真实但受控的验证，不能从 README 或单次成功推断。
6. 使用私有网页接口可能受上游服务条款限制；部署前需要确认使用范围和账号风险。

## 参考项目

- https://github.com/jin-wind/prism2api
- https://github.com/Seventy73-oss/prism-proxy
- https://github.com/lvzhentao/chatgpt-prism2api
- https://github.com/Danchuna/prismctl2api
- https://github.com/zyxzjyzjj/cpa-gptlatex-plugin
- https://github.com/Zhao73/free-astra
- https://github.com/Robinfxa/prism2api
- https://github.com/openaeon/PrismOpenAIProxy
- https://github.com/FaFengFei1961/prism-ai-gateway
- https://github.com/jiusanzhou/prism
- https://learn.chatgpt.com/docs/enterprise/gateway-compatibility
- https://learn.chatgpt.com/docs/config-file/config-reference

/cc @ranxi2001



