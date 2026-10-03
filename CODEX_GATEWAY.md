# Codex 与动态模型目录

本轮范围：模型/思考档位声明发现、按账号能力路由、Codex 0.160.0 的标准 Responses 工具闭环和真实 CLI 协议验收。只修改 oai-prism，不修改 NewAPI，不部署生产。测试状态以当前 PR Checks 为准，本文不是预先宣告测试通过。

## 1. 能力分层

| 状态 | 能证明什么 |
|---|---|
| configured | 运营者写在配置中；不是动态发现或推理验证 |
| declared | 在指定账号的模型元数据中观察到；不是模型实际能力测试 |
| stale / unavailable | 声明过期、未能取得或不满足请求，不用于动态模型路由 |
| 真实 CLI fixture 通过 | Codex 的请求/解析/本地工具确实走通；不代表真实模型会正确选择这些操作 |
| live inference / semantic validation | 必须另行执行经授权的真实上游测试。本轮不进行真实模型消费 |

不能把HTTP200、菜单里出现一个模型名或模型自报身份当成能力验证。思考档位按模型与账号记录，不假定全部模型都有low/medium/high，也不自动降级用户指定档位。

## 2. 网关开关

在现有私有配置的 `facade.gateway` 下增加：

```yaml
codex_tools: true
prompt_tools: true
catalog:
  refresh_interval: 5m
  max_age: 1h
  publish: [YOUR_OBSERVED_PUBLIC_MODEL_ID]
  sources:
    - name: account-a-models
      account_id: YOUR_EXISTING_ACCOUNT_POOL_ID
      path: /var/lib/oaiprism/catalog/account-a.json
```

`account_id` 必须对应现有账号池，且由运营者配置，不允许终端请求、client_metadata 或用户自报的 user 字段指定。每个元数据源只代表一个账号。也可以将 `path` 换为运营者控制的 `url`（HTTPS或字面回环HTTP），并通过 `bearer_env` 指定目录服务凭据环境变量。它读取的是下面的标准化快照，不会自动猜测任意上游接口形状；禁止HTTP重定向。

默认未配置sources时保持静态模型列表。新发现的模型只有加入publish后才公开；不自动修改NewAPI价格、权限、渠道或生产配置。

### 标准化快照

```json
{
  "observed_at": "<实际观察时间，RFC3339>",
  "models": [{
    "id": "<对外模型ID>",
    "upstream_id": "<已观察到的真实模型ID>",
    "display_name": "<显示名>",
    "reasoning_efforts": ["<该账号实际声明的档位>"],
    "default_effort": "<实际声明的默认档位>",
    "context_window": 0,
    "input_modalities": ["text"],
    "capabilities": {}
  }]
}
```

未知窗口填0；没有证据的档位/模态/能力不填。Codex目录导出要求已知默认档位和正数上下文窗口，因此缺少信息的模型不会被伪装成完整Codex模型。元数据不导入上游指令模板、审批规则、MCP地址或任意客户端配置。

刷新失败保留最后有效数据直至max_age过期，不把一次429/网络故障当成永久模型下架。已纳入动态目录的模型即便与静态别名同名，在目录不可用时也不会退回任意账号。

## 3. 从当前已授权浏览器观察目录

`tools/model-catalog/observe.mjs` 是只读辅助工具。它不登录、不读取/复制Cookie，不调用模型，不绕过访问控制，也不会替你选择不明确的账号。

先为单个账号准备已登录、仅回环CDP可达的专用浏览器，再指定你已经确认的元数据响应前缀：

```bash
node tools/model-catalog/observe.mjs \
  --cdp http://127.0.0.1:9222 \
  --response-prefix 'https://YOUR_OBSERVED_METADATA_HOST/YOUR_OBSERVED_PATH' \
  --output /var/lib/oaiprism/catalog/account-a.json
```

需要安装playwright-core，或用 `--playwright-module /absolute/path/to/playwright-core/index.mjs` 指定已安装模块。观察器只接受一个匹配的Prism页面；账号/页面不明确会拒绝。正常刷新该已授权页面后，它监听选定响应中的 `prism_codex_models`，也可用 `--pointer /path/to/models` 明确指定实际JSON路径。不得填入猜测的路径并宣称已经自动发现真实模型。

未知长度或超过2MiB的响应会跳过，解析失败保留旧快照。当前已实现标准化和监听代码，但没有在用户真实浏览器上验收具体元数据路径。

已有合法捕获文件可离线转换：

```bash
node tools/model-catalog/observe.mjs \
  --from-json /private/observed-metadata.json \
  --pointer /path/to/models \
  --output /var/lib/oaiprism/catalog/account-a.json
```

离线转换以文件修改时间保留观察时效，不会每次重新导入就伪造新的观察时间；复制文件时需要运营者维护正确来源时间。不要把含认证信息的原始捕获文件上传到GitHub。

## 4. 对外目录与Codex配置

认证后的目录接口：

- `/v1/models`：静态模型与已公开的、新鲜动态模型。
- `/v1/model-capabilities`：能力声明及过期状态，不暴露账号ID/凭据/目录源路径。
- `/v1/codex/models`：面向固定Codex 0.160.0的保守ModelInfo目录。

模型目录是非标准扩展接口，定制NewAPI不一定转发。可从网关受限管理网络获取目录文件，再将**推理请求**配置为经过NewAPI。不要把网关渠道Key混作终端用户的NewAPI Key。

```toml
# 独立测试profile示意；合并时保留自己的配置，不覆盖全局审批和沙箱规则。
[profiles.oaiprism]
model_provider = "oaiprism"
model = "YOUR_PUBLISHED_MODEL_ID"
model_reasoning_effort = "YOUR_DECLARED_EFFORT"
model_catalog_json = "/absolute/path/to/verified-models.json"
web_search = "disabled"

[model_providers.oaiprism]
name = "oai-prism via NewAPI"
base_url = "https://YOUR_NEWAPI_ORIGIN/v1"
wire_api = "responses"
env_key = "YOUR_NEWAPI_KEY_ENV"
```

使用前核对Codex版本对profile字段的支持；目录是本地启动加载文件，网关刷新不会自动修改已启动客户端的菜单。不要通过 `danger-full-access` 或跳过审批来掩盖工具兼容问题。

## 5. 已实现的工具协议边界

`codex_tools` 显式启用，不根据用户提示词包含exec/apply_patch字样自动触发。支持标准Responses顶层function/custom工具、一级namespace、命名工具选择、相关调用与结果回放；tool_call输出可以是文本或支持的内容块数组。call_id保持配对，不能拿function输出冒充custom输出。

custom工具支持文本、Go兼容正则完整匹配，以及指纹固定的Codex 0.160.0 apply_patch Lark语法。任意Lark语法不冒充已支持；新版Codex修改语法时需更新验证和回归。命令、文件、补丁由Codex客户端执行，网关不执行客户端传来的JavaScript/shell或直接读写客户端文件。

网关继续使用提示词→严格结果校验的工具适配；不是上游原生约束解码。保留系统/developer约束、项目上下文、工具结果和消息phase，不照搬EmpFish01中丢弃developer/system或按字节截掉中段历史的实现。

`reasoning.summary` 支持auto/concise/detailed偏好；只有上游实际返回可用的公开摘要时才输出，不凭空生成。`include: ["reasoning.encrypted_content"]` 可以作为可选输出请求出现，但没有真实加密内容就不返回；输入中的非空加密推理状态明确拒绝，不伪造解密或跨上游复用。

client_metadata只作为经过限制的诊断数据保存到请求对象，不作为身份、账号选择、系统指令或上游参数。

## 6. 验收路径

`tools/codex_contract_test.py` 配合 `tools/codex-fixture` 使用真实Codex 0.160.0，在独立临时仓库执行：读calc.py→custom apply_patch→运行单元测试→只读review。上游是固定测试计划，故障和review发现也预先定义；测试证明协议、工具执行和工作树变化，不证明模型会自主发现同一个问题。

测试使用独立CODEX_HOME、假Key和不携带GitHub/模型凭据的环境，workspace-write或read-only沙箱保持开启。review前后核对工作树，拒绝把执行失败当成功。常规SDK、上下文压缩/缓存和Go全量/race回归继续保留。

## 7. 仍然分阶段推进

本轮不是Codex所有功能完整上线。通用Files API、真实图像理解与媒体预算、任意自定义语法、Responses-lite/additional_tools、完整MCP/子代理/交互式进程矩阵、官方托管 `@codex review` 和权限审批的auto_review均不据此宣称完成。

用户图片/工具返回图片的内容块可在受支持的内联路径保留，但没有真实视觉模型验收；启用图片仍受现有用量和上下文预算限制。后续应先做真实上游小流量验收，再扩展这些功能，不应因CLI fixture通过就提高权限或自动合并代码。

一手协议参考：OpenAI Codex `rust-v0.160.0` 的 `codex-rs/protocol/src/openai_models.rs`、`codex-rs/protocol/src/models.rs` 和 `codex-rs/core/assets/tools/apply_patch.lark`；官方配置文档 https://developers.openai.com/codex/config-reference/ 。原作者归属不变，未整包复制第三方fork。
