package metrics

import (
	"bytes"
	"strconv"
	"strings"
	"testing"
)

// TestHistogram_CumulativeCorrectness 是本包最重要的一条测试。
//
// 直方图的桶必须满足 Prometheus 的累积语义：le=0.5 的值 <= le=1 的值 <= le=+Inf。
// 一旦实现里"一次观测给所有更大的桶都 +1"、Render 时又做累积，
// 同一次观测就会被重复计数，分位数整体偏大——
// 指标看起来"有数"，但全是错的，极难靠肉眼发现。
func TestHistogram_CumulativeCorrectness(t *testing.T) {
	r := New()
	h := r.Histogram("test_hist", "测试", []float64{0.1, 0.5, 1, 5})

	// 10 次 0.05 秒 + 3 次 0.3 秒 + 1 次 3 秒 + 1 次 100 秒 = 15 次
	for i := 0; i < 10; i++ {
		h.Observe(0.05, "a")
	}
	for i := 0; i < 3; i++ {
		h.Observe(0.3, "a")
	}
	h.Observe(3, "a")
	h.Observe(100, "a")

	var buf bytes.Buffer
	if err := r.Render(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	got := map[string]int{}
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "test_hist_bucket{") {
			continue
		}
		le := extractLE(line)
		v := line[strings.LastIndexByte(line, ' ')+1:]
		n, _ := strconv.Atoi(v)
		got[le] = n
	}

	want := map[string]int{
		"0.1":  10, // 只有 0.05 那 10 次
		"0.5":  13, // + 0.3 那 3 次
		"1":    13,
		"5":    14, // + 3 秒那 1 次
		"+Inf": 15,
	}
	for le, w := range want {
		if got[le] != w {
			t.Errorf("le=%s 的累积计数 = %d, want %d\n完整输出:\n%s", le, got[le], w, out)
		}
	}

	// 单调性：累积计数必须非递减。
	order := []string{"0.1", "0.5", "1", "5", "+Inf"}
	for i := 1; i < len(order); i++ {
		if got[order[i]] < got[order[i-1]] {
			t.Fatalf("累积计数非单调: le=%s 是 %d，但 le=%s 是 %d",
				order[i], got[order[i]], order[i-1], got[order[i-1]])
		}
	}
}

func extractLE(line string) string {
	i := strings.Index(line, `le="`)
	if i < 0 {
		return ""
	}
	rest := line[i+4:]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func TestCounterVec_LabelIsolation(t *testing.T) {
	r := New()
	c := r.Counter("test_counter", "测试", "path", "code")
	c.Inc("/a", "200")
	c.Inc("/a", "200")
	c.Inc("/b", "500")
	c.Inc("/b", "500")
	c.Inc("/b", "500")

	var buf bytes.Buffer
	if err := r.Render(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	if !strings.Contains(out, `test_counter{path="/a",code="200"} 2`) {
		t.Errorf("标签序列未正确区分:\n%s", out)
	}
	if !strings.Contains(out, `test_counter{path="/b",code="500"} 3`) {
		t.Errorf("标签序列未正确区分:\n%s", out)
	}
	// 绝不能把 /a 与 /b 合并。
	if strings.Contains(out, `test_counter{path="/a",code="500"}`) {
		t.Error("出现了不存在的标签组合，说明键拼接有碰撞")
	}
}

// TestCounterVec_LabelKeyNoCollision 验证键分隔符不会碰撞。
//
// 用逗号拼标签值会在"值里本身含逗号"时把两条序列合并（如 URL path），
// 导致指标静默失真。
func TestCounterVec_LabelKeyNoCollision(t *testing.T) {
	r := New()
	c := r.Counter("test_collision", "测试", "a", "b")
	c.Inc("x,y", "z")
	c.Inc("x", "y,z")

	var buf bytes.Buffer
	_ = r.Render(&buf)
	out := buf.String()

	if !strings.Contains(out, `a="x,y",b="z"`) {
		t.Errorf("第一组标签丢失:\n%s", out)
	}
	if !strings.Contains(out, `a="x",b="y,z"`) {
		t.Errorf("第二组标签丢失:\n%s", out)
	}
}

func TestRender_ProcessMetrics(t *testing.T) {
	r := New()
	var buf bytes.Buffer
	if err := r.Render(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"oaiprism_uptime_seconds",
		"oaiprism_goroutines",
		"oaiprism_memory_alloc_bytes",
		"# TYPE oaiprism_uptime_seconds gauge",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("缺少 %s", want)
		}
	}
}

func TestGaugeFunc(t *testing.T) {
	r := New()
	r.Gauge("test_gauge", "测试", func() []GaugeSample {
		return []GaugeSample{
			{Labels: []string{"idle"}, Value: 3},
			{Labels: []string{"busy"}, Value: 7},
		}
	}, "state")

	var buf bytes.Buffer
	_ = r.Render(&buf)
	out := buf.String()
	if !strings.Contains(out, `test_gauge{state="idle"} 3`) {
		t.Errorf("gauge 输出错误:\n%s", out)
	}
	if !strings.Contains(out, `test_gauge{state="busy"} 7`) {
		t.Errorf("gauge 输出错误:\n%s", out)
	}
}

func TestQuote_EscapesSpecials(t *testing.T) {
	// quote 返回的是"带引号的完整字面量"，因为调用方直接把它拼进
	// name="value" 里，不应该再自己补引号。
	cases := map[string]string{
		`a"b`:   `"a\"b"`,
		`a\b`:   `"a\\b"`,
		"a\nb":  `"a\nb"`,
		"plain": `"plain"`,
	}
	for in, want := range cases {
		if got := quote(in); got != want {
			t.Errorf("quote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScalar(t *testing.T) {
	r := New()
	c := r.Scalar("test_scalar_total", "测试计数")
	c.Inc()
	c.Add(4)

	var buf bytes.Buffer
	_ = r.Render(&buf)
	if !strings.Contains(buf.String(), "test_scalar_total 5") {
		t.Errorf("标量计数错误:\n%s", buf.String())
	}
}

func TestApp_RegistersAllMetrics(t *testing.T) {
	app := NewApp()
	var buf bytes.Buffer
	if err := app.Reg.Render(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, name := range []string{
		"oaiprism_http_requests_total",
		"oaiprism_upstream_requests_total",
		"oaiprism_facade_runs_total",
		"oaiprism_facade_first_delta_seconds",
		"oaiprism_poll_rounds_total",
		"oaiprism_account_pick_total",
		"oaiprism_capture_records_total",
	} {
		if !strings.Contains(out, name) {
			t.Errorf("App 未注册指标 %s", name)
		}
	}
}

func TestApp_ObserveUpstream(t *testing.T) {
	app := NewApp()
	app.ObserveUpstream("/api/projects", 200, 150_000_000, nil)
	app.ObserveUpstream("/api/projects", 0, 10_000_000, errFake)

	var buf bytes.Buffer
	_ = app.Reg.Render(&buf)
	out := buf.String()
	if !strings.Contains(out, `oaiprism_upstream_requests_total{path="/api/projects",code="200"} 1`) {
		t.Errorf("成功请求未记录:\n%s", out)
	}
	if !strings.Contains(out, `code="-1"`) {
		t.Errorf("网络错误应当记为 -1:\n%s", out)
	}
}

type fakeErr struct{}

func (fakeErr) Error() string { return "boom" }

var errFake = fakeErr{}
