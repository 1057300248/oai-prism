// Sentinel Turnstile dx VM —— 纯 Go 解释器（闭包式寄存器机器）。
//
// 依据 sdk.js 反混淆还原（_n 求解器）：
//   - _n 在执行指令前用 IIFE 预载 36 个内建闭包到寄存器 1-35（指令只负责 COPY 到随机键）
//   - 主循环：[op, args...] = queue.shift(); regs[op](...args) —— args 一律原样传给闭包，
//     由各闭包自己决定是否解引用（CALL/ACALL 解引用，DEFINED-CALL/FUNCDEF 原样 —— 细节关键）
//   - 队列寄存器 9；RESOLVE(3) → resolve(btoa(""+v))；异常 → resolve(btoa("步数: 错误"))；
//     500ms 超时 → resolve(""+步数)
//   - 第二层：指令流内嵌 base64 块，atob→XOR(字面量密钥)→JSON.parse 后替换队列
package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"strings"
	"time"
)

// ---------- JS 值模型 ----------

type jsObj struct {
	keys  []string
	props map[string]any
}

func newObj() *jsObj { return &jsObj{props: map[string]any{}} }

func (o *jsObj) get(k string) any {
	if o == nil {
		return nil
	}
	return o.props[k]
}

func (o *jsObj) set(k string, v any) {
	if _, ok := o.props[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.props[k] = v
}

type jsFn func(args ...any) any

type boundFn struct {
	recv *jsObj
	name string
}

// ---------- VM ----------

type VM struct {
	regs       map[float64]any
	done       bool
	setLog     []string
	result     string // resolve 的值（t = btoa(这个)）
	isTimeout  bool
	steps      int
	trace      bool
	env        *jsObj
	envData    EnvData
	scriptSrcs []string
	lsData     map[string]string
	rnd        *rand.Rand
	randSeq    []float64
	randIdx    int
	t0         time.Time
}

type EnvData struct {
	Screen       map[string]any `json:"screen"`
	Navigator    map[string]any `json:"navigator"`
	Perf         map[string]any `json:"perf"`
	DateStr      string         `json:"dateStr"`
	FontRect     map[string]any `json:"fontRect"`
	ScriptSrcs   []string       `json:"scriptSrcs"`
	WindowKeys   []string       `json:"windowKeys"`
	DocumentKeys []string       `json:"documentKeys"`
	NavProtoKeys []string       `json:"navProtoKeys"`
	DocTitle     string         `json:"docTitle"`
	LocalStorage map[string]any `json:"localStorage"`
	HistKeys     []string       `json:"histKeys"`
}

func NewVM(trace bool) *VM {
	vm := &VM{
		regs:   map[float64]any{},
		lsData: map[string]string{},
		trace:  trace,
		rnd:   rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	vm.loadEnv()
	vm.buildEnv()
	vm.preload() // 内建闭包 —— 对应 _n 的 IIFE
	return vm
}

// nextRand：回放真实序列（SENTINEL_RAND），耗尽后退回本地随机。
func (vm *VM) nextRand() float64 {
	if vm.randSeq != nil && vm.randIdx < len(vm.randSeq) {
		v := vm.randSeq[vm.randIdx]
		vm.randIdx++
		return v
	}
	return vm.rnd.Float64()
}

func (vm *VM) loadEnv() {
	if rp := os.Getenv("SENTINEL_RAND"); rp != "" {
		if raw, err := os.ReadFile(rp); err == nil {
			for _, line := range strings.Split(string(raw), "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				var f float64
				if _, err := fmt.Sscanf(line, "%g", &f); err == nil {
					vm.randSeq = append(vm.randSeq, f)
				}
			}
			fmt.Fprintf(os.Stderr, "[vm] 随机序列回放: %d 个值\n", len(vm.randSeq))
		}
	}
	raw, err := os.ReadFile(envFile("env_real.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "警告：无 env_real.json：", err)
		return
	}
	if err := json.Unmarshal(raw, &vm.envData); err != nil {
		fmt.Fprintln(os.Stderr, "警告：env 解析失败：", err)
	}
	vm.scriptSrcs = vm.envData.ScriptSrcs
}

func (vm *VM) get(k any) any {
	if f, ok := toFloat(k); ok {
		return vm.regs[f]
	}
	return k
}

func (vm *VM) setReg(k any, v any) {
	if f, ok := toFloat(k); ok {
		vm.regs[f] = v
	}
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	}
	return 0, false
}

// ---------- JS 语义工具 ----------

func jsStr(v any) string {
	switch x := v.(type) {
	case nil:
		return "undefined"
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%v", x)
	case *jsObj:
		return "[object Object]"
	case jsFn, *boundFn:
		return "function"
	}
	return fmt.Sprintf("%v", v)
}

func jsNum(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case bool:
		if x {
			return 1
		}
		return 0
	case string:
		var f float64
		fmt.Sscanf(strings.TrimSpace(x), "%g", &f)
		return f
	case nil:
		return 0
	}
	return math.NaN()
}

func jsXOR(a, b string) string {
	ar, br := []rune(a), []rune(b)
	if len(br) == 0 {
		return a
	}
	out := make([]rune, len(ar))
	for i := 0; i < len(ar); i++ {
		out[i] = ar[i] ^ br[i%len(br)]
	}
	return string(out)
}

func jsAtob(s string) string {
	clean := strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '+' || r == '/' || r == '=' {
			return r
		}
		return -1
	}, strings.TrimSpace(s))
	b, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return ""
	}
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

func jsBtoa(s string) string {
	r := []rune(s)
	b := make([]byte, len(r))
	for i, c := range r {
		b[i] = byte(c & 0xFF)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func jsJSONStringify(v any) string {
	var sb strings.Builder
	encodeJSON(&sb, v)
	return sb.String()
}

func encodeJSON(sb *strings.Builder, v any) {
	switch x := v.(type) {
	case nil:
		sb.WriteString("null")
	case bool:
		if x {
			sb.WriteString("true")
		} else {
			sb.WriteString("false")
		}
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < 1e15 {
			fmt.Fprintf(sb, "%d", int64(x))
		} else {
			fmt.Fprintf(sb, "%v", x)
		}
	case string:
		b, _ := json.Marshal(x)
		sb.Write(b)
	case *jsObj:
		sb.WriteByte('{')
		first := true
		for _, k := range x.keys {
			val := x.props[k]
			if val == nil || isFn(val) {
				continue
			}
			if !first {
				sb.WriteByte(',')
			}
			first = false
			kb, _ := json.Marshal(k)
			sb.Write(kb)
			sb.WriteByte(':')
			encodeJSON(sb, val)
		}
		sb.WriteByte('}')
	case []any:
		sb.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				sb.WriteByte(',')
			}
			encodeJSON(sb, item)
		}
		sb.WriteByte(']')
	default:
		sb.WriteString(jsStr(v))
	}
}

func isFn(v any) bool {
	switch v.(type) {
	case jsFn, *boundFn:
		return true
	}
	return false
}

func jsEq(a, b any) bool {
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok || bok {
		return aok && bok && as == bs
	}
	af, aok2 := toFloat(a)
	bf, bok2 := toFloat(b)
	if aok2 || bok2 {
		return aok2 && bok2 && af == bf
	}
	if a == nil && b == nil {
		return true
	}
	if bo, ok1 := a.(bool); ok1 {
		b2, ok2 := b.(bool)
		return ok2 && bo == b2
	}
	return false
}

// callRaw：原样传参调用（对应 dispatcher / sn/un/an 的 raw 语义）。
func (vm *VM) callRaw(f any, args []any) (r any) {
	defer func() {
		if e := recover(); e != nil {
			r = fmt.Sprint(e)
		}
	}()
	switch x := f.(type) {
	case jsFn:
		return x(args...)
	case *boundFn:
		if fn, ok := x.recv.get(x.name).(jsFn); ok {
			return fn(args...)
		}
	}
	return nil
}

// callDeref：解引用后调用（对应 Vt/rn 的 deref 语义）。
func (vm *VM) callDeref(f any, keys []any) any {
	vals := make([]any, len(keys))
	for i, k := range keys {
		vals[i] = vm.get(k)
	}
	return vm.callRaw(f, vals)
}

func (vm *VM) logf(format string, a ...any) {
	if vm.trace {
		fmt.Printf(format+"\n", a...)
	}
}

const maxStepsDefault = 200000

// ---------- 预载内建（_n 的 IIFE） ----------

func (vm *VM) preload() {
	// reg[9] = 指令队列（空）
	vm.regs[9] = []any{}
	// reg[10] = window
	vm.regs[10] = vm.env
	// reg[16] = XOR 密钥（requirements token）—— 由外部注入
	vm.regs[16] = vm.reqKey()

	// 1 XOR-in-place(n, e)
	vm.regs[1] = jsFn(func(a ...any) any {
		vm.setReg(a[0], jsXOR(jsStr(vm.get(a[0])), jsStr(vm.get(a[1]))))
		vm.logf("XOR reg[%v] ^= [%v]", a[0], a[1])
		return nil
	})
	// 2 SET(n, raw)
	vm.regs[2] = jsFn(func(a ...any) any {
		vm.setReg(a[0], a[1])
		vm.logf("SET reg[%v] = %v", a[0], brief(a[1]))
		return nil
	})
	// 3 RESOLVE(v)
	vm.regs[3] = jsFn(func(a ...any) any {
		if !vm.done {
			vm.done = true
			vm.result = jsStr(vm.get(a[0]))
			if o, ok := vm.get(a[0]).(*jsObj); ok {
				vm.dumpAcc(o)
			}
			vm.logf(">>> RESOLVE %v", brief(vm.result))
		}
		return nil
	})
	// 4 REJECT(v)
	vm.regs[4] = jsFn(func(a ...any) any {
		if !vm.done {
			vm.done = true
			vm.isTimeout = false
			vm.result = jsStr(vm.get(a[0]))
			vm.logf(">>> REJECT %v", brief(vm.result))
		}
		return nil
	})
	// 5 ADD/PUSH(n, e)
	vm.regs[5] = jsFn(func(a ...any) any {
		dst, src := vm.get(a[0]), vm.get(a[1])
		if arr, ok := dst.([]any); ok {
			vm.setReg(a[0], append(append([]any{}, arr...), src))
		} else {
			vm.setReg(a[0], jsStr(dst)+jsStr(src))
		}
		return nil
	})
	// 6 PROP(n, obj, key)
	vm.regs[6] = jsFn(func(a ...any) any {
		vm.logf("  [PROP raw] %v / %v / %v", brief(a[0]), brief(a[1]), brief(a[2]))
		v := vm.prop(vm.get(a[1]), jsStr(vm.get(a[2])))
		vm.setReg(a[0], v)
		vm.logf("PROP reg[%v] = [%v][%q] -> %v", a[0], a[1], jsStr(vm.get(a[2])), brief(v))
		return nil
	})
	// 7 CALL(fn, args...) —— 解引用后调用，结果丢弃
	vm.regs[7] = jsFn(func(a ...any) any {
		if len(a) == 0 {
			return nil
		}
		f := vm.get(a[0])
		vm.logf("CALL reg[%v] (%d args)", a[0], len(a)-1)
		return vm.callDeref(f, a[1:])
	})
	// 8 COPY(n, e)
	vm.regs[8] = jsFn(func(a ...any) any {
		vm.setReg(a[0], vm.get(a[1]))
		vm.logf("COPY reg[%v] = reg[%v]", a[0], a[1])
		return nil
	})
	// 11 SCRIPT-FIND(n, regex)
	vm.regs[11] = jsFn(func(a ...any) any {
		pat := jsStr(vm.get(a[1]))
		m := vm.findScript(pat)
		vm.setReg(a[0], m)
		vm.logf("SCRIPT-FIND reg[%v] /%s/ -> %v", a[0], pat, brief(m))
		return nil
	})
	// 12 MAP-SELF(n)
	vm.regs[12] = jsFn(func(a ...any) any {
		vm.setReg(a[0], "vm-map")
		return nil
	})
	// 13 FCALL(n, fn, args...) —— 原样传参 + 吞错
	vm.regs[13] = jsFn(func(a ...any) any {
		if len(a) < 2 {
			return nil
		}
		f := vm.get(a[1])
		func() {
			defer func() {
				if e := recover(); e != nil {
					vm.setReg(a[0], fmt.Sprint(e))
				}
			}()
			vm.callRaw(f, a[2:])
		}()
		return nil
	})
	// 14 JSON-PARSE(n, src)
	vm.regs[14] = jsFn(func(a ...any) any {
		var parsed any
		if err := json.Unmarshal([]byte(jsStr(vm.get(a[1]))), &parsed); err != nil {
			panic(fmt.Sprintf("SyntaxError: Unexpected token in JSON: %v", err))
		}
		vm.setReg(a[0], parsed)
		vm.logf("JSON-PARSE reg[%v] <- %d 字符", a[0], len(jsStr(vm.get(a[1]))))
		return nil
	})
	// 15 JSON-STR(n, e)
	vm.regs[15] = jsFn(func(a ...any) any {
		s := jsJSONStringify(vm.get(a[1]))
		vm.setReg(a[0], s)
		vm.logf("JSON-STR reg[%v] -> %.100q", a[0], s)
		return nil
	})
	// 17 ACALL(n, fn, args...) —— 解引用 + 存结果 + 吞错
	vm.regs[17] = jsFn(func(a ...any) any {
		if len(a) < 2 {
			return nil
		}
		f := vm.get(a[1])
		var r any
		func() {
			defer func() {
				if e := recover(); e != nil {
					r = fmt.Sprint(e)
				}
			}()
			r = vm.callDeref(f, a[2:])
		}()
		vm.setReg(a[0], r)
		vm.logf("ACALL reg[%v] = [%v](...) -> %v", a[0], a[1], brief(r))
		return nil
	})
	// 18 ATOB(n)
	vm.regs[18] = jsFn(func(a ...any) any {
		vm.setReg(a[0], jsAtob(jsStr(vm.get(a[0]))))
		vm.logf("ATOB reg[%v] -> %d 字符", a[0], len([]rune(jsStr(vm.get(a[0])))))
		return nil
	})
	// 19 BTOA(n)
	vm.regs[19] = jsFn(func(a ...any) any {
		vm.setReg(a[0], jsBtoa(jsStr(vm.get(a[0]))))
		return nil
	})
	// 20 EQ-CALL(n, e, fn, args...) —— 原样传参
	vm.regs[20] = jsFn(func(a ...any) any {
		if len(a) >= 3 && jsEq(vm.get(a[0]), vm.get(a[1])) {
			vm.callRaw(vm.get(a[2]), a[3:])
		}
		return nil
	})
	// 21 DELTA-CALL(n, e, tol, fn, args...) —— 原样传参
	vm.regs[21] = jsFn(func(a ...any) any {
		if len(a) >= 4 {
			d := math.Abs(jsNum(vm.get(a[0])) - jsNum(vm.get(a[1])))
			if d > jsNum(vm.get(a[2])) {
				vm.callRaw(vm.get(a[3]), a[4:])
			}
		}
		return nil
	})
	// 22 SUBVM(n, body)
	vm.regs[22] = jsFn(func(a ...any) any {
		if len(a) < 2 {
			return nil
		}
		body, _ := a[1].([]any)
		saved := vm.regs[9]
		vm.regs[9] = append([]any{}, body...)
		vm.loop()
		vm.setReg(a[0], "undefined")
		vm.regs[9] = saved
		vm.logf("SUBVM（%d 条）完成", len(body))
		return nil
	})
	// 23 DEFINED-CALL(n, fn, args...) —— 原样传参
	vm.regs[23] = jsFn(func(a ...any) any {
		if len(a) >= 2 && vm.get(a[0]) != nil {
			vm.logf("DEF-CALL reg[%v] (%d raw args)", a[1], len(a)-2)
			return vm.callRaw(vm.get(a[1]), a[2:])
		}
		return nil
	})
	// 24 BIND(n, obj, key)
	vm.regs[24] = jsFn(func(a ...any) any {
		if o, ok := vm.get(a[1]).(*jsObj); ok {
			key := jsStr(vm.get(a[2]))
			vm.setReg(a[0], &boundFn{recv: o, name: key})
			vm.logf("BIND reg[%v] = [%s].%s", a[0], brief(o), key)
		}
		return nil
	})
	// 25 AWAIT(n, e)
	vm.regs[25] = jsFn(func(a ...any) any {
		vm.setReg(a[0], vm.get(a[1]))
		return nil
	})
	// 26/28 NOOP
	vm.regs[26] = jsFn(func(a ...any) any { return nil })
	vm.regs[28] = jsFn(func(a ...any) any { return nil })
	// 27 SUB/REMOVE(n, e)
	vm.regs[27] = jsFn(func(a ...any) any {
		dst, src := vm.get(a[0]), vm.get(a[1])
		if arr, ok := dst.([]any); ok {
			out := append([]any{}, arr...)
			for i, item := range out {
				if jsEq(item, src) {
					out = append(out[:i], out[i+1:]...)
					break
				}
			}
			vm.setReg(a[0], out)
		} else {
			vm.setReg(a[0], jsNum(dst)-jsNum(src))
		}
		return nil
	})
	// 29 LT(n, e, r)
	vm.regs[29] = jsFn(func(a ...any) any {
		x, y := vm.get(a[1]), vm.get(a[2])
		xf, xok := toFloat(x)
		yf, yok := toFloat(y)
		var r any
		if xok && yok {
			r = xf < yf
		} else {
			r = jsStr(x) < jsStr(y)
		}
		vm.setReg(a[0], r)
		return nil
	})
	// 30 FUNCDEF(t, ret, params|body[, body]) —— 闭包收到的调用参数为原样
	vm.regs[30] = jsFn(func(a ...any) any {
		if len(a) < 3 {
			return nil
		}
		var params, body []any
		if arr, ok := a[3].([]any); ok {
			params, _ = a[2].([]any)
			body = arr
		} else {
			body, _ = a[2].([]any)
		}
		saveKey, retKey := a[0], a[1]
		paramsCopy := append([]any{}, params...)
		bodyCopy := append([]any{}, body...)
		vm.setReg(saveKey, jsFn(func(cargs ...any) any {
			if vm.done {
				return nil
			}
			saved := vm.regs[9]
			for i, p := range paramsCopy {
				if i < len(cargs) {
					vm.setReg(p, cargs[i]) // gn：参数原样绑定
				}
			}
			vm.regs[9] = append([]any{}, bodyCopy...)
			vm.loop()
			r := vm.get(retKey)
			vm.regs[9] = saved
			vm.logf("FUNCDEF reg[%v] 调用完 -> %v", saveKey, brief(r))
			return r
		}))
		vm.logf("FUNCDEF reg[%v]（body %d, params %d）", saveKey, len(body), len(params))
		return nil
	})
	// 33 MUL / 35 DIV
	vm.regs[33] = jsFn(func(a ...any) any {
		vm.setReg(a[0], jsNum(vm.get(a[1]))*jsNum(vm.get(a[2])))
		return nil
	})
	vm.regs[35] = jsFn(func(a ...any) any {
		x, y := jsNum(vm.get(a[1])), jsNum(vm.get(a[2]))
		if y == 0 {
			vm.setReg(a[0], float64(0))
		} else {
			vm.setReg(a[0], x/y)
		}
		return nil
	})
}

// reqKey：寄存器 16 的初始值（requirements token，dx 解密密钥）。
func (vm *VM) reqKey() string {
	if p := os.Getenv("SENTINEL_REQKEY"); p != "" {
		return p
	}
	return ""
}

// ---------- 主循环 ----------

func (vm *VM) Run(instrs []any) {
	vm.regs[9] = instrs
	vm.loop()
}

func (vm *VM) loop() {
	for vm.steps < maxStepsDefault {
		q, _ := vm.regs[9].([]any)
		if len(q) == 0 {
			return
		}
		ins := q[0]
		vm.regs[9] = q[1:]
		arr, ok := ins.([]any)
		if !ok || len(arr) == 0 {
			continue
		}
		f := vm.get(arr[0])
		if f == nil {
			vm.logf("[%3d] ?? opcode 寄存器空 %v", vm.steps, arr[0])
			vm.steps++
			continue
		}
		vm.callRaw(f, arr[1:])
		vm.steps++
	}
}

// ---------- JS 属性访问 ----------

func (vm *VM) prop(obj any, key string) any {
	switch o := obj.(type) {
	case *jsObj:
		return o.get(key)
	case string:
		switch key {
		case "length":
			return float64(len([]rune(o)))
		case "split":
			return jsFn(func(args ...any) any {
				sep := jsStr(argOr(args, 0))
				parts := strings.Split(o, sep)
				out := make([]any, len(parts))
				for i, p := range parts {
					out[i] = p
				}
				return out
			})
		case "includes":
			return jsFn(func(args ...any) any {
				return strings.Contains(o, jsStr(argOr(args, 0)))
			})
		case "match":
			return jsFn(func(args ...any) any {
				return vm.matchSimple(o, jsStr(argOr(args, 0)))
			})
		case "toString":
			return jsFn(func(args ...any) any { return o })
		}
		return nil
	case []any:
		switch key {
		case "length":
			return float64(len(o))
		case "pop":
			return jsFn(func(args ...any) any {
				if len(o) == 0 {
					return nil
				}
				last := o[len(o)-1]
				return last
			})
		}
		return nil
	}
	return nil
}

// matchSimple：JS str.match(re) 退化实现（覆盖 sentinel sdk.js 提取模式）。
func (vm *VM) matchSimple(s, pattern string) []any {
	pat := pattern
	pat = strings.TrimSuffix(pat, "(?=[?#]|$)")
	pat = strings.ReplaceAll(pat, `\`, "")
	if j := strings.Index(pat, "[^/]+"); j >= 0 {
		head := pat[:j]
		tail := pat[j+len("[^/]+"):]
		if h := strings.Index(s, head); h >= 0 {
			rest := s[h+len(head):]
			if k := strings.Index(rest, tail); k >= 0 {
				return []any{s[h : h+len(head)+k+len(tail)]}
			}
		}
		return nil
	}
	// 纯字面量
	if strings.Contains(s, pat) {
		return []any{pat}
	}
	return nil
}

func (vm *VM) findScript(pattern string) any {
	for _, src := range vm.scriptSrcs {
		if m := vm.matchSimple(src, pattern); m != nil {
			return m
		}
	}
	return nil
}

func argOr(args []any, i int) any {
	if i < len(args) {
		return args[i]
	}
	return nil
}

func brief(v any) string {
	s := jsStr(v)
	r := []rune(s)
	if len(r) > 56 {
		return fmt.Sprintf("%.56q…", string(r[:56]))
	}
	return fmt.Sprintf("%q", s)
}

func (vm *VM) dumpAcc(o *jsObj) {
	fmt.Println("--- 累积对象（t 原文结构）---")
	for _, k := range o.keys {
		fmt.Printf("  %-28s = %v\n", k, brief(o.props[k]))
	}
}

// ---------- 环境桩 ----------

func (vm *VM) buildEnv() {
	w := newObj()
	vm.env = w
	vm.t0 = time.Now()
	w.set("window", w)
	w.set("self", w)
	w.set("globalThis", w)

	sc := newObj()
	for k, v := range vm.envData.Screen {
		sc.set(k, v)
	}
	w.set("screen", sc)

	nav := newObj()
	for k, v := range vm.envData.Navigator {
		nav.set(k, v)
	}
	w.set("navigator", nav)

	pf := newObj()
	pf.set("timeOrigin", vm.envData.Perf["timeOrigin"])
	if mem, ok := vm.envData.Perf["memory"].(map[string]any); ok {
		m := newObj()
		for k, v := range mem {
			m.set(k, v)
		}
		pf.set("memory", m)
	}
	pf.set("now", jsFn(func(args ...any) any {
		return float64(time.Since(vm.t0).Microseconds()) / 1000.0
	}))
	w.set("performance", pf)

	mo := newObj()
	mo.set("random", jsFn(func(args ...any) any { return vm.nextRand() }))
	mo.set("abs", jsFn(func(args ...any) any { return math.Abs(jsNum(argOr(args, 0))) }))
	w.set("Math", mo)

	ref := newObj()
	ref.set("set", jsFn(func(args ...any) any {
		if obj, ok := argOr(args, 0).(*jsObj); ok {
			obj.set(jsStr(argOr(args, 1)), argOr(args, 2))
			vm.setLog = append(vm.setLog, fmt.Sprintf("[step %d] %s = %v", vm.steps, jsStr(argOr(args, 1)), brief(argOr(args, 2))))
		}
		return true
	}))
	ref.set("get", jsFn(func(args ...any) any {
		obj, _ := argOr(args, 0).(*jsObj)
		if obj == nil {
			return nil
		}
		return obj.get(jsStr(argOr(args, 1)))
	}))
	w.set("Reflect", ref)

	oc := newObj()
	oc.set("create", jsFn(func(args ...any) any { return newObj() }))
	oc.set("keys", jsFn(func(args ...any) any {
		if o, ok := argOr(args, 0).(*jsObj); ok {
			out := make([]any, len(o.keys))
			for i, k := range o.keys {
				out[i] = k
			}
			return out
		}
		return []any{}
	}))
	w.set("Object", oc)

	ls := newObj()
	ls.set("setItem", jsFn(func(args ...any) any {
		if obj, ok := argOr(args, 0).(*jsObj); ok {
			_ = obj
		}
		vm.lsData[jsStr(argOr(args, 0))] = jsStr(argOr(args, 1))
		vm.logf("localStorage.setItem(%q, %v)", jsStr(argOr(args, 0)), brief(argOr(args, 1)))
		return nil
	}))
	ls.set("getItem", jsFn(func(args ...any) any {
		if v, ok := vm.lsData[jsStr(argOr(args, 0))]; ok {
			return v
		}
		return nil
	}))
	// 真实条目（Object.keys(localStorage) 是 VM 的采集项！）
	for k, v := range vm.envData.LocalStorage {
		ls.set(k, v)
	}
	w.set("localStorage", ls)

	doc := newObj()
	doc.set("body", newObj())
	scripts := make([]any, 0, len(vm.scriptSrcs))
	for _, src := range vm.scriptSrcs {
		el := newObj()
		el.set("src", src)
		scripts = append(scripts, el)
	}
	doc.set("scripts", scripts)
	doc.set("createElement", jsFn(func(args ...any) any {
		if jsStr(argOr(args, 0)) == "div" {
			return vm.makeProbeDiv()
		}
		return newObj()
	}))
	doc.set("title", "Prism")
	for _, k := range vm.envData.DocumentKeys {
		if doc.get(k) == nil {
			doc.set(k, "[prop]")
		}
	}
	w.set("document", doc)

	hist := newObj()
	hist.set("length", float64(2))
	hist.set("state", nil)
	hist.set("pushState", jsFn(func(args ...any) any { return nil }))
	hist.set("replaceState", jsFn(func(args ...any) any { return nil }))
	w.set("history", hist)
	loc := newObj()
	loc.set("search", "")
	loc.set("href", "https://prism.openai.com/")
	w.set("location", loc)

	// 真实键集：Object.keys(window/document) 的规模是 VM 采集项
	// （函数属性在前，剩余键以占位串补齐 —— 键序与数量必须真实）
	for _, k := range vm.envData.WindowKeys {
		if w.get(k) == nil {
			w.set(k, "[prop]")
		}
	}
	docKeys := newObj()
	for _, k := range vm.envData.DocumentKeys {
		docKeys.set(k, "[prop]")
	}
	doc.set("__keys__", docKeys) // 供 Object.keys(document) 使用（若 VM 采集）
	if vm.envData.DocTitle != "" {
		doc.set("title", vm.envData.DocTitle)
	}
	for _, k := range vm.envData.NavProtoKeys {
		if nav.get(k) == nil {
			nav.set(k, "[prop]")
		}
	}
}

func (vm *VM) makeProbeDiv() *jsObj {
	el := newObj()
	el.set("style", newObj())
	el.set("innerText", "")
	rect := newObj()
	for k, v := range vm.envData.FontRect {
		rect.set(k, v)
	}
	el.set("getBoundingClientRect", jsFn(func(args ...any) any { return rect }))
	el.set("remove", jsFn(func(args ...any) any { return nil }))
	return el
}


// SolveT: run VM to solve t (btoa output).
func SolveT(instrs []any, trace bool) (t string, steps int, resolved bool) {
	vm := NewVM(trace)
	vm.Run(instrs)
	return jsBtoa(vm.result), vm.steps, vm.done
}

// envFile/must: path and error helpers required by vm.go.
func envFile(name string) string {
	if p := os.Getenv("SENTINEL_ASSETS"); p != "" {
		return p + string(os.PathSeparator) + name
	}
	return "C:" + string(os.PathSeparator) + "Users" + string(os.PathSeparator) + "13080" + string(os.PathSeparator) + "AppData" + string(os.PathSeparator) + "Local" + string(os.PathSeparator) + "Temp" + string(os.PathSeparator) + name
}
