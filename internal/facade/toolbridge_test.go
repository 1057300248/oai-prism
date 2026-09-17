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

// TestBridgeEnabled：Codex CLI 把工具放在 input 的 additional_tools 条目，
// 顶层 tools 为 null —— 必须以 additional_tools 判断。
func TestBridgeEnabled(t *testing.T) {
	withTools := map[string]json.RawMessage{
		"input": json.RawMessage(`[{"type":"additional_tools","tools":[]}]`),
	}
	if !BridgeEnabled(withTools) {
		t.Fatal("含 additional_tools 应启用桥")
	}
	without := map[string]json.RawMessage{
		"input": json.RawMessage(`"你好"`),
	}
	if BridgeEnabled(without) {
		t.Fatal("纯文本 input 不应启用桥")
	}
	empty := map[string]json.RawMessage{}
	if BridgeEnabled(empty) {
		t.Fatal("无 input 不应启用桥")
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
	item := customToolCallItemJSON("ctc_1", "await tools.exec_command()", 0)
	var m map[string]any
	if err := json.Unmarshal([]byte(item), &m); err != nil {
		t.Fatalf("不是合法 JSON: %v", err)
	}
	for _, k := range []string{"id", "type", "status", "call_id", "name", "input"} {
		if _, ok := m[k]; !ok {
			t.Errorf("缺少必填字段 %s: %s", k, item)
		}
	}
	if m["type"] != "custom_tool_call" || m["name"] != "exec" {
		t.Errorf("type/name 错误: %s", item)
	}
	if m["input"] != "await tools.exec_command()" {
		t.Errorf("input 应为 JS 源码字符串: %s", item)
	}
}
