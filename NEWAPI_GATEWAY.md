# NewAPI 网关接入与兼容性说明

本分支只修改 `1057300248/oai-prism`。不需要修改 NewAPI 源码；未部署生产，未读取生产账号，未执行真实上游推理。这里的“兼容”是有边界、可测试的协议兼容，不是宣称 Prism 内部服务等同于原生 OpenAI API。

## 交付来源

基线为 `master@a0bc201cc70e52e8a1fda3059b6c9d14ad8c8f89`，工作分支为 `codex/newapi-native-hardening-20261003`，PR #1。

上一轮公司电脑的未提交工作树在本轮开始时离线。本分支通过 GitHub API 和 GitHub Actions 重建、扩充、测试并提交，不是对那份离线工作树的逐字节同步。电脑恢复后应对比差异，不要 reset 或覆盖未提交内容。

## 1. 网关模式与旧工作台分开

新配置是：

```yaml
facade:
  enabled: true
  gateway:
    enabled: true
```

注意：不是上一轮离线草稿的 `gateway_mode` 字段。

开启后，公共渠道不注册 `/admin/*`、`/dashboard/*` 和 `/prism/*`，不接受 `X-Local-Workspace`、`X-Oaiprism-*`、`X-Prism-*` 或 Sentinel 覆盖头。公共 Key 不能管理账号、选择任意上游账号、读写服务器目录或调用原始代理。

旧个人工作台模式仍保留，但不能据此认为它适合公开多租户运营。HTTP 请求触发服务器本地工作区写文件的旧路径已删除。

网关默认无状态：上下文只来自本次显式 input/messages，不使用相同首句、user、prompt_cache_key 或 metadata.session_id 自动拼接缓存历史。

## 2. 能力表

| 能力 | 实现与边界 |
|---|---|
| Chat Completions 同步/流式 | 标准返回结构、工具调用结束原因、可选 usage-only 末帧和成功时单个 `[DONE]` |
| Responses 同步/流式 | 稳定 response/item/call ID，单调 sequence_number，added/delta/done/terminal 事件 |
| 字符串/消息数组、多轮文本 | 字符串不拆字符；完整历史显式传递。内部协议对历史有损，因此适配层把角色化历史作为 user 数据传入，不提升为 system 指令 |
| Function calling | 显式开启 `prompt_tools`；提示词适配 + 声明/参数 JSON Schema 校验。不是原生约束解码，服务器不执行函数 |
| 工具结果续接 | Chat tool_calls/tool 与 Responses function_call/function_call_output，校验 call_id；调用方负责执行函数并回传结果 |
| tool_choice / parallel_tool_calls | 支持 auto/none/required/指定声明函数；返回违反选择规则或禁止并行规则时失败 |
| JSON object / JSON Schema | 开启 `structured_output` 后提示词引导，再用 JSON Schema 校验；不合法输出失败，不伪装成功 |
| 输出长度参数 | 开启 `local_output_limit` 后兼容 max_tokens/max_completion_tokens/max_output_tokens，采用本地 o200k_base 可见输出裁剪。不是上游生成上限，不包含隐藏推理的硬限制 |
| stop | Chat 文本输出的本地停止字符串处理；不与工具适配混用 |
| Responses 存储/续接 | 可选，有所有者隔离、不可变快照、TTL 和容量限制；读取、删除及 input_items 分页 |
| 图片输入 | 可选 `inline_images`，只接受校验过的内联 base64 PNG/JPEG/GIF/WebP；需要上游实报 usage。默认关闭 |
| 模型发现 | `/v1/models`、`/v1/models/{id}`，只接受配置内模型 |
| 能力发现 | `/v1/capabilities` 明确返回适配方式与开关 |
| 采样参数 | temperature、top_p、seed、penalties 等未验证上游语义的控制返回 400，不静默忽略 |
| 其他能力 | 远程图片 URL、音频/视频/文件输入、embeddings、hosted tools、后台生成、encrypted reasoning、任意 item_reference 暂不支持 |

工具、结构化输出、本地输出上限和 stop 使用缓冲校验：等待期间发送 SSE 注释心跳，输出通过校验后再发送。工具 arguments 可以作为一个完整 delta 返回；不伪造逐 token 的生成速度。

遇到拒绝回答或非预期工具/JSON 格式，适配层可能返回失败，而不是推测并伪造合法工具调用。模型遵从率属于真实上游验收项目。

## 3. NewAPI 接入

从 `configs/config.newapi.example.yaml` 复制私有配置，设置经验证的 Prism 模型名和独立的上游凭据文件。启动前通过环境变量设置随机渠道 Key：

```bash
export OAI_PRISM_API_KEYS='REPLACE_WITH_A_RANDOM_CHANNEL_KEY'
./oaiprism serve -config /etc/oaiprism/gateway.yaml
```

网关配置默认绑定 `127.0.0.1:8787`。生产服务器上浏览器/传输桥必须单独运行并对网关可达；配置文件不会自动创建浏览器会话，也不会使失效的上游凭据恢复有效。

NewAPI 侧通常使用 OpenAI 类型渠道，将地址设为网关 origin，例如 `http://127.0.0.1:8787`，渠道 Key 填 `OAI_PRISM_API_KEYS` 中的一条，对外模型选配置中的 `prism-text`。检查 NewAPI 最终请求路径是 `/v1/chat/completions` 或 `/v1/responses`，避免拼成 `/v1/v1/...`。SDK 直连的 base_url 则应包含 `/v1`。

当 NewAPI 在容器中时，容器内 `127.0.0.1` 不是宿主机；应使用私有容器网络地址或受限宿主机地址。不要为解决可达性而把网关、8790 桥或8791服务无鉴权开放到公网。

首次使用建议保持：

```yaml
facade:
  gateway:
    response_store: false
    usage_policy: estimate
    prompt_tools: true
    structured_output: true
    local_output_limit: true
    inline_images: false
```

这允许共享渠道 Key 下的无状态请求。Responses 客户端每轮发送完整历史并显式 `store:false`；不要发送 previous_response_id，除非已经建立下面的可信租户身份链路。

## 4. Responses 所有权与持久化

一个 NewAPI 渠道 Key 往往被多个终端用户共用，因此它本身不足以隔离存储状态。启用 response_store 必须同时配置：

```yaml
facade:
  gateway:
    response_store: true
    tenant_header: X-Verified-Tenant
    trusted_peers: ["127.0.0.1/32"]
    store_path: /var/lib/oaiprism/responses.sqlite
    store_key_env: OAI_PRISM_RESPONSE_STORE_KEY
```

可信反向代理必须用服务端已验证身份**覆盖**租户头，不能原样透传用户提供的头，也不能给所有用户填同一个静态值。真实 TCP peer 必须命中 trusted_peers；X-Forwarded-For 不能自行建立信任。缺少头、重复头或不可信 peer 均拒绝。

不具备这条身份链路时，继续用无状态模式即可，无须改 NewAPI 源码。不要为了开启 previous_response_id 而绕过所有权校验。

store_key_env 指定的变量须包含32随机字节的 base64。密钥错误或密文损坏会明确失败。保持密钥与数据库分离备份；更换密钥、渠道 Key 或租户 ID 后原快照不会自动迁移到新身份。

存储默认内存，可选 AES-256-GCM 加密 SQLite。TTL30分钟、最多512条、总预算64MiB、单快照20MiB。到期、淘汰、无权限或不存在都不能静默续聊；同一旧响应的两个分支使用不可变快照。SQLite 面向单主机，不是多节点 Redis 集群。多实例必须使用明确一致的存储拓扑；不支持靠随机负载均衡让独立内存实例共享 previous_response_id。

GET input_items 只返回该响应的输入，不包含该响应刚生成的输出。支持 after、limit(1..100，默认20)、order(asc/desc，默认desc)，未知查询参数明确拒绝。

`store:false` 表示网关不建立 Responses 历史快照；网关模式也跳过工作台正文审计/journal。它**不是**对上游服务、外层代理、操作系统或其他系统零数据留存的承诺。数据库删除和TTL不等于取证级物理擦除。

## 5. usage 与计费

`estimate` 优先保留上游实报 usage；缺失时按 o200k_base 对可见输入/输出估算，并返回 `usage.x_oaiprism_source=estimated`。`upstream_only` 在没有可靠实报数据时失败，不编造用量。

缓存与推理明细仅在上游明确提供时返回；未知不等于0。计数类型、范围、总数和明细关系都检查，非法值不能进入成功计费数据。上游实报用量不会因为本地输出裁剪而被擅自减小。

估算不等于官方账单：Prism 的模板、隐藏推理、工具适配额外生成和图片成本可能不可见。图片缺少权威 usage 时失败，不能把 base64 长度当图片 token 成本。

响应包含 `X-Oaiprism-Usage-Policy`；实际来源通过 JSON usage 扩展字段和 `X-Oaiprism-Usage-Source` 返回，流式场景来源头是 HTTP trailer。中间代理可能丢弃 trailer，因此不能仅靠它做账单判断。

NewAPI 可能忽略扩展 provenance 字段。应给估算渠道单独配置并披露计费规则，实际检查成功/中断/失败时的预扣、结算和退款日志。此分支没有修改或验收生产 NewAPI 的账务逻辑。

## 6. 稳定性与运维

请求期限覆盖账号获取、项目创建、沙箱准备、start 和 poll；客户端更早的 deadline 被保留。已被上游接受的请求不换账号重跑；非幂等 POST 不在传输未知结果后盲目重试。上游停止使用独立短超时，只有尚未结束的已知生成才尽力停止。

网关按账号串行隔离可变工作区，每个请求使用新项目并失效本地沙箱缓存。这优先保证隔离，可能增加冷启动成本；不能将原工作台的每账号高并发设置当成已验证吞吐。建议从每账号 MaxConcurrency=1 开始，并在实测后调整账号池规模。

SSE 使用5秒注释心跳、30秒写入期限、短写/Flush错误检测，语义事件不会在 terminal 后继续发。上游接受前保留真正HTTP错误码；因此项目/沙箱冷启动期间仍需要外层代理足够长的响应头超时。关闭代理响应缓冲和SSE缓存，不重试已经开始的事件流。

`/healthz` 只代表进程存活；`/readyz` 在没有可用账号时503，但不执行真实生成，不能证明浏览器桥或模型端点完全可用。`/metrics` 仅允许内网监控访问。进程关闭会停止 Runner 的维护循环并关闭存储句柄。

项目、沙箱和凭据的上游资源生命周期仍由原传输层及上游控制。生产前需要观察实际资源回收和持续运行表现，不应仅凭内存缓存清空就宣称云端容器已销毁。

## 7. 可复现验证

```bash
go vet ./...
go test -timeout 120s -count=1 ./...
go test -timeout 180s -race ./...
CGO_ENABLED=0 go build -trimpath -o oaiprism ./cmd/oaiprism
```

GitHub Actions 的 Gateway Cloud Verification、原 CI 和 Official SDK Contract 分别保留Go测试/编译、前端检查、官方SDK证据。SDK固定Python openai 3.24.0和JavaScript openai 7.27.0。`tools/gateway-fixture`只绑定回环地址、返回固定模拟响应，不是生产模型服务。

测试包含事件ID/序号/收尾、真假失败、取消传播、禁止接受后重跑、工具schema与call_id、响应所有者隔离、快照分支/TTL/删除、加密存储重启/错误密钥、请求大小与短写、JSON Schema外部资源禁止，以及原审计中间件1MiB截断回归。

CI通过只能证明被测协议和代码路径。真实Prism模型遵从率、沙箱隔离/回收、图片实报用量、长时间稳定性、实际NewAPI账务和公网网络链路均是独立的现场验收项。本轮未用生产凭据执行这些检查。

## 8. Fork 核查（2026-10-03）

| 仓库 | 核查HEAD | 结论 |
|---|---|---|
| devImpChen/oai-prism | 2b69c4dc9a20ce0d6c4bd535e33a168b941c436c | 较早上游快照，不是完整原生兼容证据 |
| xuseny/oai-prism | f3d4c3cd3f2c3feb3885f55d3f983f22ae93f24b | 较早停止脚本修改 |
| EmpFish01/oai-prism | ef8854f223b83eac49cbbfe20866f8ed8803a01a | 浏览器sidecar、登录向导、可迁移/会话化启动有参考价值；实际Responses代码仍消费但不执行部分参数，并直接接受私有上游响应ID |

部署完整不等于协议和多租户完整。本次没有从以上fork整包复制，也没有证据断言其他未核查fork都不完整。保留原作者归属；GitHub元数据未识别原项目许可证，不能自行宣布原作者代码为MIT或重新授权。

## 一手协议参考

- https://developers.openai.com/api/docs/guides/function-calling
- https://developers.openai.com/api/docs/guides/streaming-responses
- https://developers.openai.com/api/docs/guides/structured-outputs
- https://developers.openai.com/api/reference/java/resources/responses/subresources/input_items/methods/list
- https://docs.newapi.pro/en/docs/guide/feature-guide/admin/channel
- https://docs.newapi.pro/en/docs/api/ai-model/chat/openai/createresponse
