// Package sse 是面向 Chat Completions 流式场景的 SSE 写出层。
//
// 为什么不用现成的 SSE 库：这里的瓶颈不在"能不能写 SSE"，而在"每次
// 增量写出的分配次数"。一次 4k token 的回答在高并发下会产生几十万次
// 写出调用，每次多两三个堆分配就是几十万次 GC 压力。
//
// 本包因此做三件事：
//
//  1. 缓冲区从 sync.Pool 复用，稳态下零分配；
//  2. JSON 字符串转义手写，避免 encoding/json 的反射与 map 分配；
//  3. 常量帧（如 [DONE]）预先编码成 []byte，重复写出不再拼装。
package sse

import (
	"bytes"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"unicode/utf8"
)

// 预编码的常量帧。SSE 协议里 "\n\n" 表示一次事件结束。
var (
	frameDataPrefix  = []byte("data: ")
	frameEventPrefix = []byte("event: ")
	frameTerminator  = []byte("\n\n")
	frameNewline     = []byte("\n")

	// DoneFrame 是 OpenAI 流式协议约定结束标记。
	DoneFrame = []byte("data: [DONE]\n\n")

	// CommentPing 用于保活，避免中间层（Nginx/CDN）因空闲断连。
	CommentPing = []byte(": ping\n\n")
)

var bufPool = sync.Pool{
	New: func() any {
		b := make([]byte, 0, 8192)
		return &b
	},
}

// ErrClosed 表示连接已被调用方关闭。
var ErrClosed = errors.New("sse: writer closed")

// Writer 是 SSE 写出器。
//
// 并发说明：加了 mu 之后写路径是并发安全的 —— 长等待场景需要
// "心跳 goroutine + 业务 goroutine" 同时写（例如 OAIprism 在等
// 上游 start+poll 的几分钟里必须持续发 in_progress 保活，
// 否则中间代理/客户端会因空闲判流断）。写操作本身很短，
// 锁竞争可忽略。
type Writer struct {
	mu sync.Mutex

	w  http.ResponseWriter
	fl http.Flusher

	buf    []byte
	header bool
	closed bool
	err    error

	// bytesWritten 累计写出量，用于观测"流是否真的在流动"。
	bytesWritten int64
	events       int64
}

// New 构造写出器并立即发送响应头。
//
// 关于响应头：这几个 X-Accel-* / Cache-Control 不是可选项。
// 少了 X-Accel-Buffering: no，Nginx 会把整个流缓冲到结束才吐给客户端，
// 于是"流式"退化成"一次性返回"，现象是首字延迟等于整段生成时间。
func New(w http.ResponseWriter) (*Writer, error) {
	return NewWithHeaders(w, nil)
}

// NewWithHeaders 允许附加自定义响应头。
func NewWithHeaders(w http.ResponseWriter, extra map[string]string) (*Writer, error) {
	fl, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("sse: ResponseWriter 不支持 Flush，无法流式输出")
	}

	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache, no-store, no-transform, must-revalidate")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	h.Set("Pragma", "no-cache")
	for k, v := range extra {
		h.Set(k, v)
	}
	w.WriteHeader(http.StatusOK)
	fl.Flush()

	p := bufPool.Get().(*[]byte)
	return &Writer{w: w, fl: fl, buf: (*p)[:0], header: true}, nil
}

// WriteData 写一个 data 事件。
//
// data 必须是已经序列化好的 JSON（或任意单行文本）。
func (s *Writer) WriteData(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if s.closed {
		return ErrClosed
	}
	s.buf = append(s.buf, frameDataPrefix...)
	// SSE 规范要求 data 字段内不能出现裸换行；多行要拆成多个 data: 行。
	// JSON 序列化结果天然不含裸换行（会被转义成 \n），
	// 但为了防御调用方传裸文本，这里做一次检查。
	if bytes.IndexByte(data, '\n') < 0 {
		s.buf = append(s.buf, data...)
	} else {
		s.writeMultiline(data)
	}
	s.buf = append(s.buf, frameTerminator...)
	s.events++
	return s.flushBuffer()
}

// WriteEvent 写一个带 event 名的事件。
func (s *Writer) WriteEvent(event string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if s.closed {
		return ErrClosed
	}
	if event != "" {
		s.buf = append(s.buf, frameEventPrefix...)
		s.buf = append(s.buf, event...)
		s.buf = append(s.buf, frameNewline...)
	}
	s.buf = append(s.buf, frameDataPrefix...)
	s.buf = append(s.buf, data...)
	s.buf = append(s.buf, frameTerminator...)
	s.events++
	return s.flushBuffer()
}

// WriteRaw 直接写预编码好的字节（如 DoneFrame / CommentPing）。
func (s *Writer) WriteRaw(raw []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if s.closed {
		return ErrClosed
	}
	s.buf = append(s.buf, raw...)
	return s.flushBuffer()
}

// Ping 发送保活注释（长等待期间的心跳；并发安全）。
func (s *Writer) Ping() error { return s.WriteRaw(CommentPing) }

// Done 发送 [DONE] 结束帧。
func (s *Writer) Done() error { return s.WriteRaw(DoneFrame) }

func (s *Writer) writeMultiline(data []byte) {
	start := 0
	for i := 0; i < len(data); i++ {
		if data[i] != '\n' {
			continue
		}
		s.buf = append(s.buf, data[start:i]...)
		s.buf = append(s.buf, frameNewline...)
		s.buf = append(s.buf, frameDataPrefix...)
		start = i + 1
	}
	s.buf = append(s.buf, data[start:]...)
}

// flushBuffer 把缓冲区推到客户端并立即 Flush。
//
// Flush 是流式的生命线：不 Flush 的话 net/http 会攒到 4KB 才发，
// 表现为"回答卡顿、一段一段地蹦"。
func (s *Writer) flushBuffer() error {
	if len(s.buf) == 0 {
		return nil
	}
	n, err := s.w.Write(s.buf)
	s.bytesWritten += int64(n)
	if err != nil {
		s.err = err
		return err
	}
	s.fl.Flush()
	s.buf = s.buf[:0]
	return nil
}

// Stats 返回已写出的字节数与事件数。
func (s *Writer) Stats() (bytes int64, events int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bytesWritten, s.events
}

// Err 返回第一个写错误。
func (s *Writer) Err() error { return s.err }

// Close 归还缓冲区。必须 defer 调用。
func (s *Writer) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	if cap(s.buf) > 0 && cap(s.buf) <= 1<<20 {
		b := s.buf[:0]
		bufPool.Put(&b)
	}
	s.buf = nil
}

// ---------------------------- 手写 JSON 编码 ----------------------------

// AppendJSONString 把 s 作为 JSON 字符串追加到 dst。
//
// 与 encoding/json 的区别：
//   - 不分配中间 []byte；
//   - 不做 HTML 转义（< > & 保持原样），因为 SSE 的消费方是 JSON 解析器，
//     不是 <script> 标签，多余的转义只会增加体积；
//   - 对非法 UTF-8 序列替换为 U+FFFD，保证输出始终是合法 JSON。
func AppendJSONString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); {
		c := s[i]

		// ASCII 快路径：绝大多数 token 都是 ASCII，循环体越短越好。
		if c < utf8.RuneSelf {
			if c >= 0x20 && c != '"' && c != '\\' {
				i++
				continue
			}
			dst = append(dst, s[start:i]...)
			switch c {
			case '"':
				dst = append(dst, '\\', '"')
			case '\\':
				dst = append(dst, '\\', '\\')
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			case '\b':
				dst = append(dst, '\\', 'b')
			case '\f':
				dst = append(dst, '\\', 'f')
			default:
				// 其余控制字符 < 0x20
				dst = append(dst, '\\', 'u', '0', '0')
				dst = append(dst, hexDigits[c>>4], hexDigits[c&0xF])
			}
			i++
			start = i
			continue
		}

		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			dst = append(dst, s[start:i]...)
			dst = append(dst, "\uFFFD"...)
			i++
			start = i
			continue
		}
		// \u2028 / \u2029 在 JSON 里合法，但作为 JS 源码行分隔符会出问题；
		// 转义它们成本极低，换来对各类客户端的兼容性。
		if r == '\u2028' || r == '\u2029' {
			dst = append(dst, s[start:i]...)
			dst = append(dst, '\\', 'u', '2', '0', '2', hexDigits[byte(r)&0xF])
			i += size
			start = i
			continue
		}
		i += size
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}

const hexDigits = "0123456789abcdef"

// AppendInt 以十进制追加一个整数，避免 strconv 的接口开销。
func AppendInt(dst []byte, v int64) []byte {
	if v == 0 {
		return append(dst, '0')
	}
	var tmp [20]byte
	i := len(tmp)
	neg := v < 0
	// 用 uint64 取绝对值，避免 math.MinInt64 溢出。
	var u uint64
	if neg {
		u = uint64(-(v + 1)) + 1
	} else {
		u = uint64(v)
	}
	for u > 0 {
		i--
		tmp[i] = byte('0' + u%10)
		u /= 10
	}
	if neg {
		i--
		tmp[i] = '-'
	}
	return append(dst, tmp[i:]...)
}

// AppendFloat 以更短的形式追加浮点数。
func AppendFloat(dst []byte, f float64) []byte {
	return strconv.AppendFloat(dst, f, 'f', -1, 64)
}
