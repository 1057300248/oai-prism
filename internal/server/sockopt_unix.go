//go:build !windows

package server

import "syscall"

// setRecvBuf 调整 socket 接收缓冲区。
func setRecvBuf(fd uintptr, size int) error {
	return syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, size)
}
