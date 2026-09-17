//go:build windows

package server

import "syscall"

// setRecvBuf 调整 socket 接收缓冲区。
//
// 对反代而言，接收缓冲决定了"上游吐得比我们写得快"时能吸收多少突发，
// 默认几十 KB 在高带宽低延迟链路上会造成不必要的 TCP 零窗口。
func setRecvBuf(fd uintptr, size int) error {
	return syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, size)
}
