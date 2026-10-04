# Files、图片、PDF与高级Codex工作流

本阶段只修改oai-prism，未修改NewAPI、未合并master、未部署生产，也没有调用真实/付费模型。本文件补充并更新`CODEX_GATEWAY.md`、`NEWAPI_GATEWAY.md`和`CONTEXT_CACHE.md`中较早的文件/媒体限制；未提及的限制仍然适用。

## 交付与证据

- 文件/媒体基础集成：`fc293538efe2a9bd7208962b79134bfe8f7be962`。
- 高级客户端与上游上传验证后的集成：`104cbe70ab84f54220818d4059904360adfa75f5`。
- [Advanced Codex Integration](https://github.com/1057300248/oai-prism/actions/runs/37128785935)：全仓Go记录为340通过、0失败、0跳过；vet、race及Linux amd64/arm64构建通过。真实Codex和Files SDK全部通过后才提交集成。
- 实际使用Codex CLI 0.160.0、官方Python SDK openai 3.24.0。常规`.github/workflows/codex-advanced.yml`保留回归；临时有写权限集成脚本/工作流已删除。
- 本地fixture给出确定性测试计划。客户端工具、MCP、进程、子代理和HTTP协议是真实执行，模型输出不是实时生成。因此测试不证明真实Prism模型的视觉理解、PDF解析、工具选择或review质量。

## 1. 已实现的Files API子集

| 入口 | 行为 |
|---|---|
| `POST /v1/files` | multipart上传一份文件，purpose必须为`user_data` |
| `GET /v1/files` | 当前所有者文件列表，支持purpose、limit、after、order |
| `GET /v1/files/{id}` | 所有者校验后的元数据 |
| `GET /v1/files/{id}/content` | 所有者校验后的二进制下载，不渲染上传HTML等主动内容 |
| `DELETE /v1/files/{id}` | 删除当前所有者的文件；后续访问404 |
| Responses `input_file` | 已上传的file_id，或filename+file_data内联文件 |
| Responses `input_image` | 内联data URL或当前所有者上传图片的file_id |

这是明确受限的兼容实现，不是OpenAI所有Files/Uploads功能。没有assistants/fine-tune/batch目的、分段Uploads、远程file_url、文档转换服务或任意宿主机文件路径读取。Chat Completions的原生`type:file`附件形态尚未实现；文件附件示例使用Responses。

上传和响应引用只接受当前已实现的类型：UTF-8文本/源代码、PNG/JPEG/GIF/WebP，以及有条件的PDF。DOCX、PPTX、XLSX、压缩包和可执行程序不因为扩展名存在就被宣称支持。

### 限制

- 单文件8 MiB，单图片6 MiB；引用文件展开后的单请求预算12 MiB。
- 文件默认保存24小时，可配置1小时至30天。客户端指定expires_after不能超过运营者保留上限。
- 每租户默认64份/32 MiB，全局默认128 MiB，最多1024条记录。配额不足明确拒绝，不驱逐其他用户文件。
- 图片每轴最多16384像素、总像素最多4000万，检查文件标记和尺寸；不是杀毒或完整图像语义验证。
- PDF只做有界格式封套检查，然后交给经过验证的上游文件通道；不执行PDF脚本，不在网关OCR，也不宣称完成PDF解码/杀毒。
- 文件名不得包含目录分隔符、NUL或控制字符。multipart重复字段、未知字段和超限数据拒绝。
- `/content`始终以attachment/octet-stream、nosniff、no-store和CSP sandbox下载，避免上传内容在网关源下被直接执行。

## 2. 身份、存储和删除

Files需要`facade.gateway.tenant_header`和`trusted_peers`。可信代理必须用经过验证的最终用户身份覆盖租户头；直接透传客户端自报值、给所有用户使用一个静态值或只靠共享NewAPI渠道Key，都不能提供最终用户隔离。

没有这一身份链路时保持`files.enabled:false`，仍可继续使用已经支持的无状态文本/Codex本地工具。不要为打开文件功能而放宽现有公共渠道鉴权。

存储可选内存或AES-256-GCM加密SQLite。持久化要求`files.key_env`对应变量保存32随机字节的Base64，并使用与Responses不同的数据库文件。文件内容和文件名等载荷加密，ID、作用域摘要、时间和大小用于索引。错误密钥或损坏密文明确失败，不当成“空文件”。默认数据库权限0600；应置于受保护的本地目录。

文件删除或到期后：

1. 元数据、内容下载和新引用均不可用。
2. 通过旧previous_response_id继续使用该文件的快照会失败。
3. 旧响应的input_items不能继续返回已复制的文件输入内容。
4. 已生成的独立回答，以及运营者另行启用的摘要缓存，遵循各自的保留/删除规则。删除文件不等于抹除模型曾产生的所有衍生文本。

`store:false`控制Responses快照，不删除通过Files API显式上传的文件。客户端已经取得并重新提交的内容也不能被网关远程收回。过期/删除不是取证级物理擦除或对上游数据留存的承诺。

SQLite是单主机持久化实现，不是分布式附件服务。多实例须明确共享/路由拓扑，不能让独立内存实例随机处理同一个file_id。

## 3. 解析与上游文件通道

UTF-8文本作为明确标记的用户文件数据进入模型上下文，不提升为system/developer指令。图片和PDF保留二进制数据并在执行前上传到本次隔离项目，由服务器生成随机上游文件名，不把客户端文件名当成服务器或远程项目路径。

HTTP端到端测试覆盖真实网关→账号池→Prism客户端→localhost模拟上传入口，核对：

- 图片/PDF上传前后字节完全一致。
- 同样的两个请求使用不同项目和上游文件名。
- start请求引用已上传路径，不重复携带Base64载荷。
- 上传错误不启动生成，不盲目重放，不向用户泄漏上游错误正文。
- 客户端取消上传时传到上游，请求取消后不启动生成。

这证明代码转换路径正确，不证明真实Prism服务一定能够理解上传内容。

## 4. 媒体预算与计费严格分离

二进制载荷不会按Base64字符数计入token预算。输入预算用运营者验证过的`media.image_reserve_tokens`、`media.pdf_reserve_tokens`作为保守准入预留。默认0表示未知；需要媒体预算时，未配置预留会失败，不随意猜测官方成本。

预留值不是官方token计算、不是模型窗口能力声明，也不会填入标准usage。只要请求含二进制媒体，即使usage_policy=estimate，也必须收到权威上游usage才返回成功；缺失时失败。真实模型模板、PDF页数和处理方式变化都可能使预留失准，运营者必须现场验证并保留余量。

压缩保留最早图片/PDF所在的完整用户轮次及后续历史，不将二进制Base64塞进文字摘要。不可再分割的媒体后缀仍超出预算时明确失败，而不是丢图片或削掉工具结果。文本文件经过解析后可以像普通用户文本一样参与摘要。

动态模型的图像请求进一步筛选具有image声明的新鲜账号；PDF还要求运营者的pdf_models白名单以及账号声明中的pdf_input能力。静态模型由运营者显式配置负责。目录中的declared不能自动升级为live-verified。

## 5. 真实Codex高级测试

固定版本0.160.0新增的常规验证：

| 场景 | 验证内容 |
|---|---|
| CLI `-i`附图 | 图像内容到达网关；小PNG核对SHA-256，不只检查有文件名 |
| `view_image` | Codex本地工具返回的图片像素穿过工具结果和下一轮输入 |
| stdio MCP | 真正启动受控MCP测试服务，工具返回文本+PNG并进入下一轮 |
| 交互式进程 | exec_command创建TTY进程，write_stdin使用真实session ID输入并取得ECHO输出 |
| 子代理 | multi_agent_v1.spawn_agent创建子代理；子代理实际调用网关；wait_agent取得完成结果 |
| Python Files SDK | 上传/读取/下载/列表/删除，文本与图片file_id输入，跨租户拒绝及删除后续接拒绝 |

这些测试使用独立CODEX_HOME、临时仓库和假Key。workspace-write/read-only沙箱保持开启，还运行只读写入和工作区外写入被拒绝的负向检查。中转服务器不执行客户端传来的shell、JavaScript或MCP命令。

本轮修正了测试自身的三个问题，而不是放宽生产参数校验：`-i`可变参数需要`--`分隔后续提示词；关闭共享SDK连接不能影响主客户端；子代理agent_id应从工具结果JSON解出，不能抓取工具item的第一个UUID。后者有独立回归测试。

本次子代理验证是实际出现的multi_agent_v1，不等于所有未来多代理协议。MCP验证的是本地stdio工具文本/图片返回，不是托管MCP、OAuth、所有资源类型或所有服务器实现。

## 6. 配置与使用

`configs/config.newapi.example.yaml`继续保持安全默认值：files和images默认关闭，媒体预留为0，PDF名单为空。文件和媒体按各自需要开启，不能将测试fixture的预算数值照搬为生产模型参数。

在已经建立可信最终用户身份的环境，可使用以下相关项（其余沿用完整配置）：

```yaml
facade:
  gateway:
    tenant_header: X-Verified-Tenant
    trusted_peers: ["127.0.0.1/32"]
    files:
      enabled: true
      path: /var/lib/oaiprism/files.sqlite
      key_env: OAI_PRISM_FILES_KEY
      ttl: 24h
      owner_files: 64
      owner_bytes: 33554432
      total_bytes: 134217728
```

由可信反代注入租户头，终端SDK不要自行伪造身份。应用应以自己的最终用户Key访问NewAPI/受控入口，而不是把网关渠道Key分发给所有客户：

```python
import os
from openai import OpenAI

with OpenAI(api_key=os.environ["CLIENT_API_KEY"],
            base_url=os.environ["CLIENT_API_BASE"], max_retries=0) as client:
    with open("notes.md", "rb") as source:
        uploaded = client.files.create(file=source, purpose="user_data")
    try:
        response = client.responses.create(
            model=os.environ["PUBLISHED_MODEL"],
            input=[{"role": "user", "content": [
                {"type": "input_file", "file_id": uploaded.id},
                {"type": "input_text", "text": "Summarize the notes."}
            ]}],
            store=False,
        )
        print(response.output_text)
    finally:
        client.files.delete(uploaded.id)
```

NewAPI必须把文件相关路由、multipart、file_id及最终用户身份正确送到同一网关作用域。不同渠道自己的file_id并不通用；在没有做这一版本/路由核查前，不能声称无需配置便能经任意NewAPI上传文件。必要时只在受控测试入口验收，不修改生产路由来掩盖问题。

Codex本地读写仓库仍是客户端工具，通常不依赖Files API；基础provider配置见`CODEX_GATEWAY.md`。媒体能力目录是本地加载快照，变更后要重新加载相应客户端。

## 7. 未由本轮验证覆盖

真实Prism账号的目录数据源、思考档位效果、KV缓存命中、视觉/PDF理解、输出usage完整性、长时间沙箱资源回收，以及生产NewAPI预扣/结算/退款，仍需授权现场测试。没有伪造原生加密compaction、托管`@codex review`或权限审批auto_review。任意Lark、Responses-lite/additional_tools、远程文件/图片URL、音视频及Office格式仍不冒充支持。

本地readonly review的真实客户端回归继续保留，但本轮没有配置自动发布GitHub审查评论、提交修复、批准权限或自动合并的生产工作流。

一手语义参考：
- https://developers.openai.com/api/docs/guides/file-inputs
- https://developers.openai.com/api/docs/guides/images-vision
- https://developers.openai.com/codex/config-reference/
- OpenAI Codex仓库标签rust-v0.160.0的protocol模型、MCP与multi_agent_v1工具声明。
