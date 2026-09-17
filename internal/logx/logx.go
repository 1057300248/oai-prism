// Package logx 提供极轻量的结构化日志封装。
//
// 设计取舍：直接复用标准库 log/slog，不引入 zap/zerolog。
// 原因是在本项目的热路径（SSE 增量转发）中我们几乎不打日志，
// 日志开销集中在请求边界；slog 的 JSON handler 已经足够，
// 且少一个依赖 = 少一份供应链风险。
package logx

import (
	"log/slog"
	"os"
	"strings"
)

// Level 映射配置文件中的字符串级别。
func Level(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug", "trace":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error", "fatal":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// Setup 初始化全局 logger。format 支持 json / text。
func Setup(level, format string) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level: Level(level),
		// 源码位置在代理场景里价值不高，但排查协议问题很关键，
		// 因此保留在 debug 级别下（AddSource 的代价是每次调用取一次 runtime.Caller）。
		AddSource: Level(level) == slog.LevelDebug,
	}

	var h slog.Handler
	if strings.EqualFold(format, "text") {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}

	l := slog.New(h)
	slog.SetDefault(l)
	return l
}
