# NewAPI 网关接入与兼容性说明

本分支只修改 `1057300248/oai-prism`，未合并 master、未部署生产、未读取生产凭据或调用真实付费上游。基本 Chat/Responses 接入与服务器端自动摘要不需要修改 NewAPI 源码；新端点/字段能否被定制 NewAPI 转发仍需按其实际版本核对。

**上下文压缩与缓存现已实现，具体行为、开关、使用示例和计费边界见 [CONTEXT_CACHE.md](CONTEXT_CACHE.md)。** 它不是 OpenAI 原生加密 compaction，也不保证真实 Prism 缓存命中。

## 交付与恢复来源

基线 `master@a0bc201cc70e52e8a1fda3059b6c9d14ad8c8f89`；工作分支 `codex/newapi-native-hardening-20261003`；PR #1。公司电脑离线时通过 GitHub API/Actions 重建并验证，不是对 Windows 未提交工作树逐字节同步。电脑恢复后应先对比未提交改动，不能 reset 覆盖。

## 1. 网关与个人工作台分开

```yaml
facade:
  enabled: true
  gateway:
    enabled: true
```

这是当前嵌套配置，不是早期离线草稿的 gateway_mode。开启后不注册 `/admin/*`、`/dashboard/*`、`/prism/*`；禁止 X-Local-Workspace、X-Oaiprism-*、X-Prism-* 和 Sentinel 覆盖头。公共渠道 Key 不获得账号管理、指定私有上游状态、任意服务器目录读写或原始代理能力。

旧工作台模式仍保留，但不因此获得公共多租户安全保证；HTTP触发服务器本地工作区写文件的旧路径已删除。

默认不保存 Responses 历史，也不根据首句/user/cache标签偷偷拼接缓存历史。开启摘要仅处理本次显式输入或已通过所有者校验的 previous_response_id 历史；prompt_cache_key 是调度/缓存提示，不是用户身份。

## 2. 能力与限制

| 能力 | 实现和边界 |
|---|---|
| Chat Completions | 同步/流式、工具结束原因、可选usage-only末帧、成功时单个DONE |
| Responses | 同步/流式、稳定response/item/call ID、单调序号和完整生命周期事件 |
| 文本历史 | 统一稳定JSON-lines渲染；去除分页UUID；保持工具关联和角色，历史数据不提升为系统指令 |
| Function calling | 开启prompt_tools后提示词适配+声明与Schema校验；支持工具结果、选择及并行约束；服务器不执行函数 |
| JSON结构化输出 | 开启structured_output后提示词引导+JSON/Schema校验；不是原生约束解码 |
| max_tokens等输出限制 | 开启local_output_limit后按本地o200k_base可见输出裁剪；不限制上游实际生成或隐藏推理费用 |
| Chat stop | 本地停止字符串处理，不与工具适配混用 |
| 上下文预算/摘要 | 可配置模型窗口、输出预留、阈值、保留最近轮次；完整工具组边界；多窗口分块摘要；失败不丢原历史 |
| compact/input_tokens | `/v1/responses/compact` 返回明确标注的普通摘要消息，不伪造encrypted_content；input_tokens为本地估算 |
| 摘要缓存 | 可选、可信租户隔离、不可变前缀、单次并发构建、TTL/容量限制；不冒充上游KV缓存 |
| Prompt Cache控制 | 作用域化key用于账号亲和；native_models白名单控制原生字段透传；不保证上游命中 |
| Responses存储 | 可选所有者隔离、不可变快照、读取/删除/previous_response_id/input_items分页；可选加密SQLite |
| 内联图片 | 开启inline_images后只接受校验过的base64 PNG/JPEG/GIF/WebP，要求权威usage；不能与本地图片token预算混用 |
| 模型/能力发现 | `/v1/models`、`/v1/models/{id}`、`/v1/capabilities` |
| 未支持 | 未验证的采样参数、远程图片URL、音视频/文件输入、embeddings、hosted tools、background、encrypted reasoning、任意item_reference |

工具、结构化输出和本地输出控制采用缓冲校验，通过后才发出内容；等待时有SSE心跳，不伪造逐token生成速度。摘要发生在最终生成之前，额外调用会计入用量明细。格式不符或模型拒绝可能返回失败，不能伪造合法工具结果。

## 3. NewAPI配置

复制 [configs/config.newapi.example.yaml](configs/config.newapi.example.yaml) 到私有配置路径，填入已验证的模型名、独立凭据文件，并设置随机渠道Key：

```bash
export OAI_PRISM_API_KEYS='REPLACE_WITH_A_RANDOM_CHANNEL_KEY'
./oaiprism serve -config /etc/oaiprism/gateway.yaml
```

NewAPI采用OpenAI类型渠道，Base URL通常填网关origin（例如http://127.0.0.1:8787），渠道Key使用上述私有Key，模型选公开配置名。检查最终路径为 `/v1/chat/completions` 或 `/v1/responses`，避免双重v1。SDK直连base_url应含v1。

容器内的127.0.0.1不是宿主机，应配置私有网络可达地址；不要为方便连接把网关、传输桥或浏览器服务无鉴权暴露公网。上游浏览器/传输服务需要单独正常运行，配置不会自动创建有效会话或绕过上游控制。

首次接入保持response_store:false、summary_cache:false、inline_images:false。需要自动摘要时按真实上游容量配置context窗口并显式开启；不能凭公开模型别名猜窗口。原生缓存字段只对真实验证过的native_models启用。

内部自动摘要不依赖客户端新端点，但压缩可能增加延迟和模型费用。独立compact/input_tokens端点及context_management请求字段是否通过NewAPI路由需另验，本项目不会假定转发成功。

## 4. 所有者隔离和存储

多个最终用户可能共用同一渠道Key，因此开启response_store或summary_cache要求可信租户头和真实TCP对端CIDR。反代必须基于已经验证的最终用户身份**覆盖**头，不能原样转发用户自填值，也不能给所有人相同静态值；X-Forwarded-For不建立信任。没有此条件就保持这两种存储关闭，使用显式历史和无缓存摘要。

Responses默认内存，可选AES-256-GCM加密SQLite。配置store_path与store_key_env后，变量必须包含32随机字节的base64。密钥错误或密文损坏明确失败；更换渠道Key/租户ID/加密密钥不会自动迁移旧快照。

Responses快照TTL30分钟、最多512条、总预算64MiB、单条20MiB；历史分支不可变。input_items只列该响应输入，不含刚生成的输出，支持after、limit(1..100/默认20)、order(asc/desc/默认desc)。上下文压缩后的输入项仍有有效分页ID。

摘要缓存是另外的进程内缓存，最多128条/8MiB，默认TTL30分钟，重启失效；SQLite Responses存储不是多节点Redis集群。多个实例需要明确一致的状态拓扑，不能让独立内存实例随机接力previous_response_id。

store:false只控制Responses快照，不关闭运营者单独启用的summary_cache。网关模式跳过工作台正文审计和journal；这不是对上游、外层代理或整个系统零留存的承诺，TTL/删除也不是物理擦除保证。

## 5. 用量与失败处理

estimate模式优先用上游实报，缺失则使用明确标注的本地估算；upstream_only缺少可靠用量时失败。cached_tokens、cache_write_tokens和reasoning_tokens仅在上游提供时返回，严格检查整数、范围、总和与别名冲突，不从本地缓存命中推算。

已修复带request_id的typed响应分支丢失payload.usage的问题。输入预算/估算与实际发送的网关渲染共享实现，但Prism隐藏模板与推理仍可能不可见。

自动摘要成功时标准usage合计本次摘要调用与最终生成，并附x_oaiprism_context明细；来源可能upstream/estimated/mixed。本地摘要缓存命中不重复计入先前构建成本，也不是模型缓存折扣。独立compact只计本次摘要调用，无新调用为0。无法从失败/断连且无用量的请求恢复官方完整账单。

来源与上下文统计通过正文扩展和响应头/trailer输出；代理可能丢trailer，不能只靠头做账。生产NewAPI可能忽略扩展或使用不同缓存写入计价，本分支没有现场验收预扣/结算/退款。启用自动摘要前必须确定业务费用处理规则。

## 6. 运维与测试

请求期限覆盖账号选择、项目/沙箱、start/poll及摘要；保留客户端更早deadline。已接受生成不换账号重跑，非幂等POST不在结果未知后盲目重试；断开后尽力停止已知且未结束的上游任务。每请求隔离项目和可变工作区，缓存账号亲和不取消隔离。建议从每账号并发1实测吞吐，不能把缓存优化等同于已实现高并发。

SSE心跳5秒、写期限30秒，短写/Flush错误传播；失败不发成功终态。创建项目等首包前阶段需要外层代理足够响应头超时。禁用代理SSE缓冲、缓存和开始输出后的重试。

healthz是进程存活；readyz检查可用账号但不做真实生成，不能证明浏览器桥/模型全链路可用。metrics仅供内网监控。Runner维护循环和存储在关闭时释放，摘要缓存关闭后不再接受或重新填充工作；本地失效缓存不代表上游沙箱已物理销毁。

```bash
go vet ./...
go test -timeout 120s -count=1 ./...
go test -timeout 180s -race ./...
CGO_ENABLED=0 go build -trimpath -o oaiprism ./cmd/oaiprism
```

GitHub Actions保留Go、竞态、双架构、Dashboard、官方Python/JavaScript SDK测试。SDK使用localhost固定fixture，不调用真实模型。旧证据见 [GATEWAY_VERIFICATION.md](GATEWAY_VERIFICATION.md)，新压缩/缓存说明见 [CONTEXT_CACHE.md](CONTEXT_CACHE.md)；最新结果以PR当前HEAD的Checks为准。

CI不能证明真实摘要事实完整性、模型遵从率、原生加密压缩可用、真实缓存命中、官方账单、沙箱回收、生产网络或长期SLA。未进行生产部署。

## 7. Fork核查记录（2026-10-03）

| 仓库 | 核查HEAD | 结果 |
|---|---|---|
| devImpChen/oai-prism | 2b69c4dc9a20ce0d6c4bd535e33a168b941c436c | 较早上游快照 |
| xuseny/oai-prism | f3d4c3cd3f2c3feb3885f55d3f983f22ae93f24b | 较早停止脚本修改 |
| EmpFish01/oai-prism | ef8854f223b83eac49cbbfe20866f8ed8803a01a | 浏览器/登录/部署便利性有参考价值，但已读Responses实现不能证明完整原生参数和多租户语义 |

未整包复制上述fork，也不断言未核查fork全部不完整。保留原作者归属；原项目GitHub元数据未识别许可证，不自行声明原作者代码为MIT或重新授权。

一手参考：
- https://developers.openai.com/api/docs/guides/function-calling
- https://developers.openai.com/api/docs/guides/streaming-responses
- https://developers.openai.com/api/docs/guides/structured-outputs
- https://developers.openai.com/api/docs/guides/compaction
- https://developers.openai.com/api/docs/guides/prompt-caching
- https://docs.newapi.pro/en/docs/guide/feature-guide/admin/channel
