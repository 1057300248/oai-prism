// Package capture 录制上下游报文，用于校准协议映射。
//
// 存在的理由很实际：这套 Prism 内部接口的字段名我们没有权威文档，
// 只能靠"发出去什么、收回来什么"来反推。
// 与其靠猜，不如让代理自己把真实流量记下来，然后照着改配置。
//
// 关键约束：录制绝不能拖慢热路径。
// 因此采用"有界 channel + 丢包"策略——队列满了就丢弃并计数，
// 而不是阻塞请求。宁可丢几条日志，也不能让生产流量变慢。
package capture

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oai-prism/oaiprism/internal/metrics"
	"github.com/oai-prism/oaiprism/internal/prism"
)

// Record 是一条录制记录。
type Record struct {
	Time       time.Time         `json:"time"`
	Direction  string            `json:"direction"` // request | response
	Method     string            `json:"method,omitempty"`
	Path       string            `json:"path"`
	Status     int               `json:"status,omitempty"`
	Account    string            `json:"account,omitempty"`
	Attempt    int               `json:"attempt,omitempty"`
	DurationMS float64           `json:"duration_ms,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	Truncated  bool              `json:"truncated,omitempty"`
}

// Options 是录制选项。
type Options struct {
	Enabled      bool
	Dir          string
	MaxBody      int64
	Redact       bool
	Sample       float64
	IncludePaths []string
	ExcludePaths []string
	QueueSize    int
}

// Recorder 是异步录制器。
type Recorder struct {
	opts Options
	app  *metrics.App

	ch      chan Record
	dropped atomic.Int64
	written atomic.Int64

	mu      sync.Mutex
	file    *os.File
	day     string
	closeMu sync.Once
	done    chan struct{}

	seq atomic.Uint64
}

// 敏感头，一律打码。
var sensitiveHeaders = map[string]struct{}{
	"authorization":       {},
	"cookie":              {},
	"set-cookie":          {},
	"x-api-key":           {},
	"api-key":             {},
	"proxy-authorization": {},
	"x-oai-prism-key":     {},
}

// New 构造录制器。Enabled 为 false 时返回 nil，调用方据此跳过所有录制开销。
func New(opts Options, app *metrics.App) *Recorder {
	if !opts.Enabled {
		return nil
	}
	if opts.Dir == "" {
		opts.Dir = "captures"
	}
	if opts.MaxBody <= 0 {
		opts.MaxBody = 4 << 20
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = 4096
	}
	if opts.Sample <= 0 {
		opts.Sample = 1
	}
	if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
		// 目录建不出来就直接禁用，不要让代理因为日志问题启动失败。
		return nil
	}

	r := &Recorder{
		opts: opts,
		app:  app,
		ch:   make(chan Record, opts.QueueSize),
		done: make(chan struct{}),
	}
	go r.loop()
	return r
}

// Enabled 判断是否启用。
func (r *Recorder) Enabled() bool { return r != nil }

// Dropped 返回丢弃条数。
func (r *Recorder) Dropped() int64 {
	if r == nil {
		return 0
	}
	return r.dropped.Load()
}

// Written 返回落盘条数。
func (r *Recorder) Written() int64 {
	if r == nil {
		return 0
	}
	return r.written.Load()
}

// shouldRecord 做路径过滤与采样。
func (r *Recorder) shouldRecord(path string) bool {
	if r == nil {
		return false
	}
	for _, ex := range r.opts.ExcludePaths {
		if ex != "" && strings.HasPrefix(path, ex) {
			return false
		}
	}
	if len(r.opts.IncludePaths) > 0 {
		hit := false
		for _, inc := range r.opts.IncludePaths {
			if inc != "" && strings.HasPrefix(path, inc) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	if r.opts.Sample < 1 {
		// 用递增序号做确定性采样，避免引入随机数开销。
		if float64(r.seq.Add(1)%1000)/1000.0 >= r.opts.Sample {
			return false
		}
	}
	return true
}

// enqueue 非阻塞投递。
func (r *Recorder) enqueue(rec Record) {
	if r == nil {
		return
	}
	select {
	case r.ch <- rec:
	default:
		r.dropped.Add(1)
	}
}

func (r *Recorder) sanitize(h map[string]string) map[string]string {
	if !r.opts.Redact {
		return h
	}
	for k := range h {
		if _, ok := sensitiveHeaders[strings.ToLower(k)]; ok {
			h[k] = "***REDACTED***"
		}
	}
	return h
}

func headerMap(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) == 0 {
			continue
		}
		out[k] = strings.Join(v, ", ")
	}
	return out
}

// RecordRequest 实现 prism.Recorder。
func (r *Recorder) RecordRequest(meta prism.Meta, header http.Header, body []byte) {
	if r == nil || !r.shouldRecord(meta.Path) {
		return
	}
	r.enqueue(Record{
		Time:      meta.Started,
		Direction: "request",
		Method:    meta.Method,
		Path:      meta.Path,
		Account:   meta.AccountID,
		Attempt:   meta.Attempt,
		Headers:   r.sanitize(headerMap(header)),
		Body:      r.body(body),
	})
}

// RecordResponse 实现 prism.Recorder。
func (r *Recorder) RecordResponse(meta prism.Meta, header http.Header, body []byte) {
	if r == nil || !r.shouldRecord(meta.Path) {
		return
	}
	r.enqueue(Record{
		Time:       time.Now(),
		Direction:  "response",
		Method:     meta.Method,
		Path:       meta.Path,
		Status:     meta.Status,
		Account:    meta.AccountID,
		DurationMS: float64(meta.Duration.Microseconds()) / 1000.0,
		Headers:    r.sanitize(headerMap(header)),
		Body:       r.body(body),
	})
}

// RecordRaw 供原样反代通道使用（它不走 prism.Client）。
func (r *Recorder) RecordRaw(direction, method, path string, status int, account string, header http.Header, body []byte) {
	if r == nil || !r.shouldRecord(path) {
		return
	}
	r.enqueue(Record{
		Time:      time.Now(),
		Direction: direction,
		Method:    method,
		Path:      path,
		Status:    status,
		Account:   account,
		Headers:   r.sanitize(headerMap(header)),
		Body:      r.body(body),
	})
}

func (r *Recorder) body(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	if int64(len(b)) > r.opts.MaxBody {
		return string(b[:r.opts.MaxBody])
	}
	return string(b)
}

// loop 是唯一的写盘协程。
func (r *Recorder) loop() {
	defer close(r.done)
	for rec := range r.ch {
		if err := r.write(rec); err != nil {
			r.dropped.Add(1)
			continue
		}
		r.written.Add(1)
		if r.app != nil {
			r.app.CaptureWritten.Inc()
		}
	}
}

// write 按天切分文件写入。
func (r *Recorder) write(rec Record) error {
	day := rec.Time.Format("2006-01-02")

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.file == nil || r.day != day {
		if r.file != nil {
			_ = r.file.Close()
		}
		name := filepath.Join(r.opts.Dir, fmt.Sprintf("capture-%s.jsonl", day))
		f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return err
		}
		r.file = f
		r.day = day
	}

	buf, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	buf = append(buf, '\n')
	if _, err := r.file.Write(buf); err != nil {
		return err
	}
	// 每条都 sync 太贵，靠内核缓冲即可；崩溃丢最后几条可以接受。
	return nil
}

// Close 停止录制并落盘。
func (r *Recorder) Close() {
	if r == nil {
		return
	}
	r.closeMu.Do(func() {
		close(r.ch)
		<-r.done
		r.mu.Lock()
		if r.file != nil {
			_ = r.file.Sync()
			_ = r.file.Close()
			r.file = nil
		}
		r.mu.Unlock()
	})
}

// ---------------------------- 回放 ----------------------------

// ReplayFile 加载抓包文件，供离线校准与单测使用。
func ReplayFile(path string) ([]Record, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []Record
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var rec Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			return nil, fmt.Errorf("第 %d 行解析失败: %w", i+1, err)
		}
		out = append(out, rec)
	}
	return out, nil
}

// Summarize 汇总一份抓包，输出每个端点的字段名清单。
//
// 这是本包最有用的一个函数：把真实抓包变成"协议字段表"，
// 你据此填 YAML 里的 schema 段，就不用靠猜了。
func Summarize(records []Record) string {
	type endpoint struct {
		method string
		path   string
		fields map[string]struct{}
		status map[int]struct{}
	}
	byPath := map[string]*endpoint{}
	for _, rec := range records {
		key := rec.Method + " " + normalizePath(rec.Path)
		e, ok := byPath[key]
		if !ok {
			e = &endpoint{method: rec.Method, path: normalizePath(rec.Path), fields: map[string]struct{}{}, status: map[int]struct{}{}}
			byPath[key] = e
		}
		if rec.Status != 0 {
			e.status[rec.Status] = struct{}{}
		}
		if rec.Direction != "request" && rec.Direction != "response" {
			continue
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(rec.Body), &m); err != nil {
			continue
		}
		for k := range m {
			e.fields[k] = struct{}{}
		}
	}

	var sb strings.Builder
	sb.WriteString("抓包协议摘要\n")
	sb.WriteString("============\n")
	keys := make([]string, 0, len(byPath))
	for k := range byPath {
		keys = append(keys, k)
	}
	sortStrings(keys)
	for _, k := range keys {
		e := byPath[k]
		fmt.Fprintf(&sb, "\n%s\n", k)
		if len(e.status) > 0 {
			fmt.Fprintf(&sb, "  状态码: ")
			for s := range e.status {
				fmt.Fprintf(&sb, "%d ", s)
			}
			sb.WriteString("\n")
		}
		fields := make([]string, 0, len(e.fields))
		for f := range e.fields {
			fields = append(fields, f)
		}
		sortStrings(fields)
		fmt.Fprintf(&sb, "  字段(%d): %s\n", len(fields), strings.Join(fields, ", "))
	}
	return sb.String()
}

func normalizePath(p string) string {
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}
	segs := strings.Split(strings.Trim(p, "/"), "/")
	for i, s := range segs {
		if len(s) == 36 || len(s) >= 24 && strings.Count(s, "-") >= 2 {
			segs[i] = "{id}"
		}
	}
	return "/" + strings.Join(segs, "/")
}

func sortStrings(s []string) {
	// 插入排序：这里只有几十个元素，且几乎已部分有序。
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
