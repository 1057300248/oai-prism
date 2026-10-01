package facade

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestExtractExecBlock 覆盖围栏提取的三种形态。
func TestExtractExecBlock(t *testing.T) {
	// 1) 正常块
	js, ok := extractExecBlock("前言\n```codex-exec\nawait tools.exec_command({cmd:\"ls\"})\n```\n后记")
	if !ok || !strings.Contains(js, "tools.exec_command") {
		t.Fatalf("正常块提取失败: ok=%v js=%q", ok, js)
	}
	if strings.Contains(js, "前言") {
		t.Errorf("不应包含块外内容: %q", js)
	}

	// 2) 没有块
	if _, ok := extractExecBlock("纯文本回复，没有任何围栏"); ok {
		t.Fatal("纯文本不应提取出块")
	}

	// 3) 未闭合（流式截断场景）：取剩余内容
	js, ok = extractExecBlock("```codex-exec\ntext('hi')")
	if !ok || !strings.Contains(js, "text('hi')") {
		t.Fatalf("未闭合块应尽力提取: ok=%v js=%q", ok, js)
	}

	// 4) 不认别的围栏名（普通代码块不是工具调用）
	if _, ok := extractExecBlock("```js\nawait tools.exec_command()\n```"); ok {
		t.Fatal("普通 js 围栏不应被当成工具调用")
	}
}

// TestEnsureExecJS 覆盖"模型输出裸 shell 而非 JS"的自动包装。
//
// 实测背景：模型经常无视"输出 JS"的要求，直接把 bash 命令写进围栏，
// 进了 V8 就是 SyntaxError，然后陷入死循环。代理层兜底是必须的。
func TestEnsureExecJS(t *testing.T) {
	// 1) 裸命令 → 包装成 exec_command
	wrapped := ensureExecJS("printf '%s' 'X' > f.txt")
	if !strings.Contains(wrapped, "tools.exec_command") {
		t.Errorf("裸命令应被包装: %q", wrapped)
	}
	if !strings.Contains(wrapped, `printf '%s' 'X' > f.txt`) {
		t.Errorf("命令内容应原样保留: %q", wrapped)
	}

	// 2) 多行命令也整体保留
	multi := "set -eu\nprintf 'a' > f\ncat f"
	wrapped = ensureExecJS(multi)
	if !strings.Contains(wrapped, "cat f") {
		t.Errorf("多行命令应整体保留: %q", wrapped)
	}

	// 3) 已经是 JS → 原样透传
	js := "const r = await tools.exec_command({cmd: 'ls'});\ntext(r);"
	if got := ensureExecJS(js); got != js {
		t.Errorf("JS 应原样透传: %q", got)
	}
}

// TestBridgeEnabled：Codex CLI 有两条工具声明路径（见 BridgeEnabled 注释）。
// 早期只认路径 A，导致走路径 B 的客户端桥静默失效（模型退回上游沙箱执行，
// 本地拿不到文件）。这里把两条路径与"不该误伤"的场景都钉住。
func TestBridgeEnabled(t *testing.T) {
	// 路径 A：additional_tools 条目
	withTools := map[string]json.RawMessage{
		"input": json.RawMessage(`[{"type":"additional_tools","tools":[]}]`),
	}
	if !BridgeEnabled(withTools) {
		t.Fatal("含 additional_tools 应启用桥")
	}

	// 路径 A 续：已有 custom_tool_call 往返（Codex 独有形状）
	withCustom := map[string]json.RawMessage{
		"input": json.RawMessage(`[{"type":"custom_tool_call","name":"exec","input":"..."}]`),
	}
	if !BridgeEnabled(withCustom) {
		t.Fatal("含 custom_tool_call 应启用桥")
	}

	// 路径 B：标准 tools 字段 + Codex 独有工具特征
	withStandardTools := map[string]json.RawMessage{
		"input": json.RawMessage(`[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]`),
		"tools": json.RawMessage(`[{"type":"custom","name":"exec","description":"run a command"}]`),
	}
	if !BridgeEnabled(withStandardTools) {
		t.Fatal("顶层 tools 含 exec 应启用桥（路径 B）")
	}
	// 关键回归：新版 CLI 的工具名是 exec_command（不是 exec）——
	// 早期全名匹配 `"name":"exec"` 会漏判，这里钉住。
	withExecCommand := map[string]json.RawMessage{
		"input": json.RawMessage(`[{"type":"message","role":"user"}]`),
		"tools": json.RawMessage(`[{"type":"custom","name":"exec_command","description":"Runs a command"},{"type":"custom","name":"write_stdin"}]`),
	}
	if !BridgeEnabled(withExecCommand) {
		t.Fatal("顶层 tools 含 exec_command 应启用桥（路径 B，新版命名）")
	}

	withApplyPatch := map[string]json.RawMessage{
		"input": json.RawMessage(`[{"type":"message","role":"user"}]`),
		"tools": json.RawMessage(`[{"type":"custom","name":"apply_patch"}]`),
	}
	if !BridgeEnabled(withApplyPatch) {
		t.Fatal("顶层 tools 含 apply_patch 应启用桥（路径 B）")
	}

	// 不该误伤：普通 API 调用方的 function 工具
	plainFunctions := map[string]json.RawMessage{
		"input": json.RawMessage(`[{"type":"message","role":"user"}]`),
		"tools": json.RawMessage(`[{"type":"function","name":"get_weather","parameters":{}}]`),
	}
	if BridgeEnabled(plainFunctions) {
		t.Fatal("普通 function 工具不应启用桥（否则破坏正常 function calling）")
	}
	// 不该误伤：空 tools / null / 纯文本
	for name, raw := range map[string]map[string]json.RawMessage{
		"空 tools":   {"input": json.RawMessage(`[{"type":"message"}]`), "tools": json.RawMessage(`[]`)},
		"null":      {"input": json.RawMessage(`[{"type":"message"}]`), "tools": json.RawMessage(`null`)},
		"纯文本 input": {"input": json.RawMessage(`"你好"`)},
		"无 input":   {},
	} {
		if BridgeEnabled(raw) {
			t.Fatalf("%s 不应启用桥", name)
		}
	}
}

// TestExecToolName：工具名必须从请求里动态提取。
//
// 背景：CLI v0.154 工具名是 exec，v0.159 改成 exec_command。
// 名字用错时客户端直接拒绝（"unsupported custom tool call: exec"），
// 模型看不到执行结果，反复要求用户重发内容 —— 表现得像上下文丢失。
func TestExecToolName(t *testing.T) {
	cases := []struct {
		name string
		raw  map[string]json.RawMessage
		want string
	}{
		{
			"新版 CLI（exec_command）",
			map[string]json.RawMessage{"tools": json.RawMessage(`[{"type":"custom","name":"exec_command"},{"name":"write_stdin"}]`)},
			"exec_command",
		},
		{
			"旧版 CLI（exec）",
			map[string]json.RawMessage{"tools": json.RawMessage(`[{"type":"custom","name":"exec"}]`)},
			"exec",
		},
		{
			"路径 A（additional_tools 内含名字）",
			map[string]json.RawMessage{"input": json.RawMessage(`[{"type":"additional_tools","tools":[{"name":"exec"}]}]`)},
			"exec",
		},
		{
			"都不认识时兜底 exec",
			map[string]json.RawMessage{"input": json.RawMessage(`"hi"`)},
			"exec",
		},
	}
	for _, c := range cases {
		if got := ExecToolName(c.raw); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestBridgeInputItems 覆盖 Codex input 的翻译：
// 消息保留、工具调用回放为 assistant、工具结果回放为 user。
func TestBridgeInputItems(t *testing.T) {
	raw := json.RawMessage(`[
		{"type":"additional_tools","tools":[]},
		{"type":"message","role":"developer","content":[{"type":"input_text","text":"be helpful"}]},
		{"type":"message","role":"user","content":[{"type":"input_text","text":"make a file"}]},
		{"type":"custom_tool_call","call_id":"c1","name":"exec","input":"await tools.exec_command()"},
		{"type":"custom_tool_call_output","call_id":"c1","output":"[CLIENT RESULT]\nfile written\n[/CLIENT RESULT]"},
		{"type":"reasoning","summary":[]}
	]`)
	items := bridgeInputItems(raw, "base")
	if len(items) == 0 {
		t.Fatal("翻译结果为空")
	}

	all := ""
	for _, it := range items {
		for _, c := range it.Content {
			all += c.Text + "\n"
		}
	}
	if !strings.Contains(all, "bridge") || !strings.Contains(all, "codex-exec") {
		t.Error("桥指令必须注入（含 codex-exec 契约）")
	}
	if !strings.Contains(all, "be helpful") {
		t.Error("developer 消息应保留")
	}
	if !strings.Contains(all, "make a file") {
		t.Error("user 任务应保留")
	}
	if !strings.Contains(all, "await tools.exec_command()") {
		t.Error("custom_tool_call 应回放为 assistant 文本（上游需要自己的操作记忆）")
	}
	if !strings.Contains(all, "file written") {
		t.Error("custom_tool_call_output 应回放为 user 文本")
	}
	if strings.Contains(all, "additional_tools") {
		t.Error("additional_tools 不应出现在发给上游的文本里")
	}
}

// TestCustomToolCallItemJSON 校验 custom_tool_call 条目形状
// 与 Codex CLI 源码 ResponseItem::CustomToolCall 反序列化需求对齐
// （codex-rs/protocol/src/models.rs: call_id/name/input 为必填）。
func TestCustomToolCallItemJSON(t *testing.T) {
	item := customToolCallItemJSON("ctc_1", "await tools.exec_command()", "exec_command")
	var m map[string]any
	if err := json.Unmarshal([]byte(item), &m); err != nil {
		t.Fatalf("不是合法 JSON: %v", err)
	}
	for _, k := range []string{"id", "type", "status", "call_id", "name", "input"} {
		if _, ok := m[k]; !ok {
			t.Errorf("缺少必填字段 %s: %s", k, item)
		}
	}
	// name 必须与传入一致（动态工具名：CLI 版本间 exec -> exec_command）
	if m["type"] != "custom_tool_call" || m["name"] != "exec_command" {
		t.Errorf("type/name 错误: %s", item)
	}
	if m["input"] != "await tools.exec_command()" {
		t.Errorf("input 应为 JS 源码字符串: %s", item)
	}
}
