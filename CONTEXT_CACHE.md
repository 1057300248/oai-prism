# 上下文压缩、缓存与用量

本说明对应 PR #1 的上下文/缓存迭代。代码检查点 `ca5b70f052eb60993dd177183705111a45d673a6` 在 [Context Cache Integration](https://github.com/1057300248/oai-prism/actions/runs/37106933352) 完成全仓 `go vet`、307项 Go 测试（0失败、0跳过）、竞态测试以及 Linux amd64/arm64 构建。SDK 与后续提交的最终结果以当前 PR Checks 为准，不能使用旧提交通过记录替代新提交验证。

## 1. 三种状态不能混淆

| 类型 | 实现 | 计费含义 |
|---|---|---|
| Responses 历史存储 | 按所有者读取不可变历史快照，支持 previous_response_id | 客户端少传历史，不代表模型少处理历史 |
| 网关摘要缓存 | 复用同租户、同语义前缀的摘要，避免再次调用摘要模型 | 命中时没有新的摘要调用，不冒充上游 cached_tokens |
| 上游 Prompt Cache | 稳定提示词前缀、账号亲和；对运营者验证过的模型透传缓存控制 | 只有上游实报的 cached_tokens/cache_write_tokens 才是上游缓存用量 |

没有开启完整回答重放缓存，不会因为请求相似而直接返回另一个请求的旧答案。缓存参数不是授权身份，原始缓存标签不直接转发给上游。

## 2. 稳定提示词与实际输入预算

`RenderInput` 同时用于真实 Prism 适配层、输入 token 估算和缓存相关逻辑，避免一个地方统计原始 messages、另一个地方实际发送更长的封装内容。

渲染时去掉只用于网关分页/存储的随机 item ID，并将工具 call_id 规范化为保持关联关系的局部序号；公开响应仍保留真实 API ID。角色、文字、工具名、参数及结果不会因规范化而丢失。历史采用稳定 JSON-lines，不因追加一轮而重写整个前缀。系统/developer 约束单独保留；assistant/tool 历史仍是低信任数据，不提升为 system 指令。

计数使用本地 o200k_base。它包含网关实际渲染的文本封装，但不是 Prism 隐藏模板、隐藏推理或官方账单的精确测量。图片预算不能可靠估算，启用上下文预算时遇到图片会明确拒绝，而不是按 base64 长度估计图片成本。

输入预算为：

```text
已验证模型上下文窗口
- max(输出预留, 请求的本地输出上限)
- 安全余量
```

窗口必须由运营者根据真实上游验证后配置，代码不根据公开模型别名猜测。默认关闭上下文预算；开启时每个配置模型必须有512到2,000,000范围内的窗口值。

## 3. 压缩行为

开启 `context.auto_compact` 后，在生成之前检查历史，默认在输入预算的80%触发。也可以通过 Responses 请求的 `context_management` 显式指定阈值：

```json
{
  "model": "prism-text",
  "input": "本轮输入或完整消息历史",
  "store": false,
  "context_management": [
    {"type": "compaction", "compact_threshold": 60000}
  ]
}
```

以上阈值只是语法示例，不表示此模型已验证支持相应窗口。服务器必须先开启 context.enabled 并配置正确窗口。

压缩保留完整系统约束和最近的若干 user 轮次；function_call 与对应结果、并行调用组不能被从中切开。压缩独立端点允许保留末尾尚未完成的工具调用；普通生成入口仍要求此前调用已提供结果。

较早历史作为低信任证据交给同一配置模型生成摘要，要求保留任务、事实、决定、精确路径/错误码、已完成工作和未完成事项。摘要模型没有通过网关执行工具的权限。超出一个窗口的旧历史按安全轮次边界分块摘要，每一块都重新测量输入预算，不把超长历史一次性塞给摘要模型。

摘要目标默认1024 token、最近保留4轮、每次请求最多8次摘要调用。返回空摘要、拒绝、工具调用、不完整结果、超过摘要目标、不减少输入或仍然超预算，都明确失败；原请求历史不被覆盖，不静默删消息。单个不可分割历史轮次本身超过摘要预算时也会失败，需要调用方提供更小的上下文。

摘要是有损的模型输出，代码只能验证边界和大小，不能证明每个事实都完整保留。重要原始证据仍应由业务系统保存，不能用摘要替代审计记录。

## 4. Compact 与输入计数端点

```text
POST /v1/responses/compact
POST /responses/compact
POST /v1/responses/input_tokens
```

compact 接受 model、input、instructions，以及本实现允许的 tools/reasoning/previous_response_id/缓存控制字段。它返回 `object: response.compaction` 和可继续作为输入的标准消息项，同时明确标记：

```json
{
  "x_oaiprism_native_compaction": false,
  "x_oaiprism_context": {
    "implementation": "gateway_summary"
  }
}
```

**这是兼容调用方式的网关摘要模式，不是 OpenAI 原生加密 compaction。** 不制造 `type: compaction` 或伪造 `encrypted_content`，也不承诺客户端必须接受原生加密项的场景可用。官方原生服务的自动压缩可发生在生成过程中，本实现是在开始最终生成前压缩。

将返回的整个 output 作为下一次 input 的基础，然后追加新的用户消息。不要再同时附上已被摘要替代的原始旧历史。输入 instructions 被保留为标准系统消息，避免在显式 compact 后丢失。

input_tokens 返回 `object: response.input_tokens` 和 `input_tokens`，并标记 `x_oaiprism_source: estimated`、`x_oaiprism_tokenizer: o200k_base_rendered`；不调用模型。

本仓库提供这些路径，但未修改用户的定制 NewAPI 路由。NewAPI 是否转发 compact/input_tokens 或保留 context_management 需要按实际版本验证。内部自动压缩可通过服务器配置触发，不依赖客户端新增字段或 NewAPI 新端点。

## 5. 安全启用配置

默认示例关闭自动摘要和摘要缓存，避免未配置窗口时改变上下文，或未经说明增加模型调用。

下面仅演示一个**假设已验证窗口为131072**的配置；数值必须替换为真实上游能力，不是模型能力声明：

```yaml
facade:
  gateway:
    enabled: true
    response_store: false
    context:
      enabled: true
      window_tokens: 131072
      model_windows: {}
      output_reserve: 4096
      safety_margin: 2048
      auto_compact: true
      trigger_tokens: 0        # 0 = 输入预算的80%
      keep_last_turns: 4
      summary_tokens: 1024
      max_summary_calls: 8
      summary_cache: false
      cache_ttl: 30m
    prompt_cache:
      affinity: true
      native_models: []       # 没有真实验证前不要加模型
```

按模型配置时 model_windows 的键是网关公开模型名，优先于 window_tokens。调用方指定的 compact_threshold 不能超过已配置模型窗口，执行时还会受实际输入预算限制。

摘要缓存必须另外启用可信租户身份：

```yaml
facade:
  gateway:
    tenant_header: X-Verified-Tenant
    trusted_peers: ["127.0.0.1/32"]
    context:
      summary_cache: true
```

这段是合并进前面配置的字段，不是完整配置。可信反向代理必须根据已验证的最终用户身份覆盖租户头，不能原样转发用户自填值，也不能给所有终端用户相同静态值。校验真实TCP对端，不信任用户自填X-Forwarded-For。没有这条身份链路仍可使用不缓存摘要的自动压缩，无需为基本功能改变 NewAPI 源码。

摘要缓存为进程内存：最多128条、8MiB、默认TTL30分钟；不可变内容、滚动前缀指纹、模型/路由/推理强度/系统约束/工具定义/算法版本隔离。同一前缀的并发构建合并，等待者能独立取消；构建失败或异常不留下永久等待项，不缓存失败结果。进程关闭后不再接受新缓存工作，重启丢失缓存，多节点不共享它。

`store:false` 控制 Responses 快照，不自动关闭运营者单独开启的摘要缓存。需要网关不保留摘要时应同时设置 summary_cache:false；不要把这两个开关当成上游或全链路零留存承诺。过期记录不会再命中，内存物理清理在后续缓存访问或关闭时完成，删除并非取证级擦除。

## 6. 上游缓存控制

`prompt_cache_key` 现在不再丢弃。它与认证作用域、模型/路由、推理配置和稳定指令一起生成HMAC作用域键，用于账号调度亲和；原始用户标签不会直接成为上游租户标识。账号忙、失效或池调度可改变所选账号，亲和不是固定命中的保证，也不会复用可变工作区。

默认 `native_models: []`，不将未经验证的控制发送给 Prism。对实际验证过的公开模型名设置白名单后，才透传作用域化的 prompt_cache_key 及以下支持的控制：

```json
{"prompt_cache_retention": "in_memory"}
```

或 `24h`；另一种配置为：

```json
{"prompt_cache_options": {"mode": "implicit", "ttl": "30m"}}
```

两类控制不能混用。显式断点模式、prewarm:true、未知TTL/字段均拒绝。非白名单模型收到 retention/options 也明确拒绝；只带 prompt_cache_key 可以作为账号亲和提示使用，响应头说明本次采用 scoped-affinity 而非 native-parameters-forwarded。

白名单是运营者已完成验证的声明，不是本程序自动发现能力。测试已覆盖字段从API入口经过Runner和Prism客户端到达本地模拟上游，但没有使用真实Prism账号证明原生缓存被命中。只有实报缓存统计和实测延迟能支持实际命中结论。

## 7. 用量与监控

Chat 与 Responses 都保留上游明确提供的 cached_tokens 和 cache_write_tokens，按各自的 prompt_tokens_details/input_tokens_details 返回；未知保持缺失，不自行填零。整数类型、非负、总量、读写不重叠以及Chat/Responses别名冲突会检查。

本轮还修复了 typed PrismEnvelope 分支没有复制 response.payload.usage 的问题：带request_id的终态也能拿到正确的实报输入/输出与缓存明细，不再错误回退成估算。

自动压缩会额外调用模型。普通生成成功时，标准usage输入/输出为本次真实发生的摘要调用与最终生成之和，附 `x_oaiprism_context` 明细；来源可能为 upstream、estimated 或 mixed。摘要命中不重复加入历史构建成本。某阶段缓存明细未知时不能把未知阶段假定为0来生成总缓存折扣。

独立 compact 端点的usage只表示本次摘要构建成本；无操作/缓存命中时为0，并标记来源none。失败时返回已知的压缩调用/用量报告；断连或上游没给usage时无法据此恢复官方完整账单。

报告包含 original_input_tokens、effective_input_tokens、input_budget、summary_calls、summary_cache_hits、摘要输入/输出用量及最终生成明细。HTTP头提供 X-Oaiprism-Context-Before/After、X-Oaiprism-Summary-Calls、X-Oaiprism-Summary-Cache-Hits，流式时必要值通过trailer输出；JSON/SSE正文中的报告是主要依据，代理可能丢掉trailer。

不要把压缩减少的token数或摘要缓存命中数写成模型cached_tokens。NewAPI可能忽略扩展字段，也可能不支持某些缓存写入计价；本轮未修改或现场验收生产NewAPI预扣、结算、退款规则。启用付费摘要之前须明确业务计费策略。

## 8. 验证与已知边界

Go测试覆盖稳定前缀、作用域键、真实传输字段、typed与fallback用量解析、token预算、分块摘要、原历史保护、并行工具边界、重复/跨租户/修改系统约束的缓存行为、失败/取消/异常/关闭、TTL/容量、压缩后分页ID，以及流式失败和正确终态。

官方Python/JavaScript SDK测试调用compact、复用output继续聊天、验证重复摘要不重复记费、context_management自动压缩和缓存控制；测试只连接回环fixture，没有真实上游消费。

仍不能用mock CI证明摘要事实完整性、真实模型遵从率、官方加密压缩可用性、上游缓存命中率、实际输入token/隐藏推理账单、生产NewAPI账务或长期沙箱资源回收。Context/Cache能力和限制可通过 `/v1/capabilities` 查询。

一手语义参考（不代表Prism内部端点自动支持）：
- https://developers.openai.com/api/docs/guides/compaction
- https://developers.openai.com/api/docs/guides/prompt-caching
