# Prism Bridge 取长补短：传输适配与受控增量续接

本文对应 oai-prism 的 opt-in 公共网关，不替换 NewAPI，不部署或改动生产服务。

参考了 `yyyllllming/prism-bridge@8ae73c03c31f411838f77f660eace092701490f8` 的上传、网页会话与 additional_tools 设计线索；实现重新写入现有 Go 架构，未整包复制其 Python 服务或 GUI。原作者归属保持不变。

这些是**可配置、经过本地模拟上游和真实客户端回归的兼容实现**。真实 Prism 的上传/会话协议、指令遵从、视觉理解、账号状态和计费未在本轮使用生产凭据验证。示例保持保守默认值，不以某个 fork 的硬编码模型或约86KB经验值当成官方保证。

## 1. 配置：全部放在 facade.gateway.bridge

```yaml
facade:
  gateway:
    bridge:
      upload_mode: multipart         # 兼容旧传输；可选 raw，须先验证上游
      max_start_bytes: 0             # 0=不启用此额外上游字节限制，不取消公共请求上限
      instruction_placement: system  # 可选 user_relay，见下文
      additional_tools: false        # 需要 codex_tools + prompt_tools
      continuation:
        enabled: false
        verified_models: []          # 运营者现场确认过的公开模型名
        ttl: 10m                     # 私有游标可复用期限；最长1h
        session_header: ""           # 可选客户端会话标签，不是租户身份
        action_id: ""                # 可选固定的已观察Next action ID；空=有界发现
```

这是配置片段，不含认证、模型、上游桥或账户文件。完整基础见 `configs/config.newapi.example.yaml`。模型与思考档位仍遵循现有声明目录及校验，不做 xhigh→high 等静默降级。

## 2. 上传传输：显式选择，不自动试错

`multipart` 保留已有上游文件上传行为。`raw` 将已校验的 PNG/JPEG/GIF/WebP/PDF 字节直接放入请求体，设置真实媒体类型、生成的ASCII文件名和 x-prism-file-* / project-id / edit-access 头。下游客户端的 `/v1/files` 上传仍是其正常 multipart API；这个开关只改变**网关到Prism**的传输格式。

raw 成功后引用 `/prism-uploads/{生成文件名}`。返回非JSON成功正文、错误信封、超限响应、或明确返回不同项目路径时失败。上传失败不切换另一种格式，不把“丢掉图片之后得到文字答案”当作成功。

新增网关→账号池→Prism客户端→localhost模拟上游的测试，检查图片/PDF字节一致、准确项目路径进入 start、上传错误不重试另一种格式。此前 multipart 回归继续保留。**真实上游是否采用此路径必须另外验证**；上传200本身也不能证明模型实际读到了图片。

已解析附件的所有者、撤销、格式、大小和媒体实报usage限制继续生效，参见 `FILES_MEDIA.md`。增量续接只上传当前新增条目的附件；没有建立跨租户/跨会话共享附件缓存。

## 3. additional_tools：兼容入口，不是宽松工具执行器

开启 `additional_tools` 后，Responses input 中以下条目被提取并合并到本次顶层工具集合：

```json
{"type":"additional_tools","tools":[{"type":"function","name":"read","parameters":{"type":"object"}}]}
```

提取后仍由原来的 function/custom/namespace 解析与 Schema/格式验证处理。合并超限、重复定义、无实际输入、未知字段或不合法声明都拒绝；不会按名字猜成shell命令，不会自动包装成JavaScript执行，不会把上游沙箱已执行动作再次交给客户端执行。

本兼容模式把每次请求视为**完整工具声明集**，不从隐式会话补回未声明的工具。`configuration_update` 和尚未定义语义的增量配置仍拒绝，不能宣称完整 Responses-lite 已实现。

## 4. 指令放置与传输字节预算

### 完整指令保留

默认仍保留现有 system 指令和角色化历史。可选 `user_relay` 在用户传输正文开头追加一份完整、JSON编码的指令副本，同时不删除正常 system 条目。此模式是给会丢失角色字段的私有传输做显式兼容，不是真正的原生角色优先级实现；是否有效必须现场验证。

不按2000字符丢弃 instructions，不截短 developer 规则，不裁切自由文本工具描述来伪装兼容。指令放置模式参与提示词和摘要缓存指纹；预算计算与实际发送使用同一渲染实现。重复副本会增大输入，不能宣传成减少token。

### 双层字节限制

模型 token 窗口与上游 HTTP JSON 限额不同。`max_start_bytes` 为0时不增加此限制；非零接受4096..16777216字节，应按运营者自己的观察配置。

第一层在选择可用续接游标后测量渲染输入的JSON字节，不把单独上传的媒体Base64当作start文本。第二层在Prism start前对**加入私有元数据和上传文件路径后的实际JSON**再次检查。超限返回400、`context_length_exceeded`、阶段 `transport_budget`，不发送该次生成POST。

第二层检查发生在项目/沙箱/附件准备之后，因此可能已经创建上游资源；不能把“未开始模型生成”解释成“什么上游操作都没发生”。不自动把大输入切成多轮ACK；已有摘要压缩仍是显式功能，摘要子请求现在也继承相同传输配置和字节预算，单个不可分历史轮次超限时不先消耗一次摘要调用。

## 5. 受控上游增量续接

以前公共 Responses 存储只负责服务端保存和重放历史。本功能在条件满足时进一步复用真正的Prism会话，只发送新条目，并回传对应的上游 response ID 与 listen snapshot。

### 启用前置条件

必须同时配置 `facade.use_sandbox=true`、`response_store=true`、可信租户头/真实TCP peer、`continuation.enabled=true` 和 `verified_models`。存储和所有者要求没有因本功能放宽。

每次需要续接的请求都必须 `store:true`。默认不启用本功能；`store:false` 保持隔离、完整显式历史路径。首次没有previous ID也可创建一个带所有者的上游会话；后续只接受：

- 本所有者的 `previous_response_id`；或
- 明确配置的客户端会话标签，加完整的历史重放。

不会只看“相同开头/相似历史”自动合并两个任务。客户端会话标签仅在可信租户内部区分任务，不提供身份授权。一个活跃上游会话不允许改绑另一标签；分支应使用新标签或只给旧previous ID建立独立分支。

### 会话创建与状态

新会话使用已观察的 `createProjectConversation` Next server action，不伪造一个UUID后声称可以续接。action_id为空时，有界读取当前项目页面及同源 `/_next/static/*.js`，最多16份脚本、总预算4MiB，要求唯一匹配此命名action；不执行下载的JavaScript，不访问外部脚本域名。站点构建变更、发现不到或响应含糊会失败；可以由运营者提供已观察的新action ID。

浏览器/HTTP传输桥必须能够以对应授权账号访问页面、脚本和该action。没有自动登录、凭据导入或通用浏览器崩溃恢复服务。Next action不是稳定公开API，这一适配不消除上游改版风险。

### 所有者、会话头和账号版本

私有游标绑定公开响应所有者、账号、项目、会话、末次payload response ID、listen snapshot、账号工作区版本及凭据摘要。轮询request_id绝不能替代终态payload.id。

历史指纹包含角色、顺序、工具命名空间/参数/结果和实际图片/文件内容；不会把“同样有一张图片”当成同一历史。API显示用的随机item ID及等价工具关联标签不污染模型提示词。

同一游标在飞时返回409；正常完成、Schema/usage通过、公共快照保存并成功发送终态后才安装新游标。失败、取消、状态未知、不完整输出、响应删除或关闭服务都不留下可复用的新游标。过程不自动重放已受理的生成。

同一账号发生任何其他隔离请求都会推进工作区版本。凭据刷新/轮换也会令旧游标失效；一次请求从项目创建到上传/生成固定使用同一不可变凭据快照，避免中途混用身份。

过期、编辑历史、工具/指令/模型改变等在计划阶段可改走新隔离全量请求。账号版本在执行前检查发现过期会返回 `upstream_cursor_stale` 409，不执行此轮；调用方明确重试时才可用现有公共历史重新建立会话。不要把409无限重试，也不要强制继续旧沙箱。

### 保留、拓扑与费用

私有游标只在进程内保存，最多64条，默认可复用10分钟。过期清理由后续访问或关闭触发，不是取证级瞬时物理擦除。原公共Responses快照仍按自己的TTL/容量/加密配置工作。

重启不会恢复私有游标或沙箱凭据：公共历史仍可用时回到全量路径。容量满时返回503，不悄悄驱逐正在执行的会话。

**同一上游账号必须由这个网关实例独占执行。** 进程内版本无法识别另一个网关实例或人工浏览器同时使用账号导致的上游状态变更。本实现不是多节点会话调度系统，不适合共享账号跨实例随机负载均衡。

增量续接减少重复传输和部分准备工作，不等于原生KV缓存命中，不代表历史免费。上游实报usage仍原样校验；估算规则和额外摘要成本仍按 `CONTEXT_CACHE.md` 明示。不要将HTTP请求变小直接换算成输入账单减少。

服务可能在上游创建项目和会话，本轮没有自动删除云端项目/聊天记录，也不保证上游零留存。公开响应/文件删除与所有衍生数据物理擦除不是一回事。

## 6. 诊断与可选现场检查

认证接口 `GET /v1/transport-status` 返回生效的上传模式、指令模式、字节预算、功能开关及匿名汇总游标计数，不返回账号ID、action ID、凭据或快照。`live_upstream_verified` 保持false，不把配置白名单当成现场证明。建议仅从受限管理网络访问诊断接口。

响应头 `X-Oaiprism-Continuation` 标记 stateless、new-stored-conversation 或 delta。错误通过 `X-Oaiprism-Stage` 和错误/事件扩展 `x_oaiprism_stage` 区分 account、project、sandbox、workspace_sync、conversation_create、upload、start、poll、continuation、transport_budget，并用 x-request-id 定位。流已经开始后以事件内字段为准，不能再改变HTTP状态。

`tools/bridge_canary.py` 默认只请求模型目录和传输状态：

```bash
# 在私有shell中设置 OAI_PRISM_CANARY_KEY，勿提交或贴出它。
python tools/bridge_canary.py --base-url https://YOUR_GATEWAY/v1 --model YOUR_MODEL
```

只有运营者显式允许，才发送指令保留测试，以及可选的第二次续接测试：

```bash
python tools/bridge_canary.py --base-url https://YOUR_GATEWAY/v1 --model YOUR_MODEL \
  --tenant-header X-Verified-Tenant --tenant operator-canary \
  --allow-model-calls --check-continuation
```

此直接检查必须从已授权、配置匹配的可信管理来源运行。生产多租户流量仍应由认证代理覆盖租户头，不允许终端用户自行声称身份。

脚本最多发两次Responses POST，不自动重试或跟随重定向；远程明文HTTP需要额外明确允许，禁用隐式环境代理。报告只保留状态、耗时、用量和限定诊断头，不输出提示词、答案正文、Cookie或API Key。成功拿到ID的测试快照会尽力删除，清理失败单独报告；上游云端资源不因此被删除。网关若另行配置自动摘要，内部调用成本不能用“两个HTTP请求”封顶。

该工具检查指令和历史标记，不验证模型身份、视觉理解或原生缓存效果。本轮只跑其离线单测，没有执行上述真实环境命令。NewAPI未必转发诊断扩展和会话标签，现场应分别检查网关直连和实际NewAPI渠道，而不是假定两个路径一致。

## 7. 验证与边界

核心源码集成 `4e79fd06325c8c358bdee9f44448e29eab09c887` 在 Bridge Transport Integration (run 37178347951) 通过370项Go记录、vet/race、Linux amd64/arm64构建及既有真实Codex高级fixture。

后续源码 `3185020a65dccddf425675e6b00c47f29a46f215` 在 Bridge Final Hardening (run 37179112061) 通过375项Go记录、6项canary离线测试、vet/race和双架构构建后提交。最终常规五组CI应以当前PR Checks为准；中途失败/取消运行不是完成证据。

测试覆盖raw字节与路径、无自动上传fallback、严格additional_tools、系统约束不截断、真实JSON字节边界、同源唯一action、终态快照、增量只发新内容、同账号插入请求、凭据轮换、变图、分叉、取消/失败失效、关闭后不恢复游标及摘要传输预算。真实Codex基础和高级回归继续保留；模型输出使用固定localhost测试计划。

没有照搬长指令丢弃、思考档位降级、工具别名猜测执行、图片失败后跳过、无界队列、按多轮ACK隐藏额外成本或未知结果自动重跑。没有新增默认公共网关、自动审批或自动合并。
