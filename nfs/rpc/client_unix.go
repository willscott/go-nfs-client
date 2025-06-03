//go:build darwin || dragonfly || freebsd || linux || nacl || netbsd || openbsd || solaris

// Copyright © 2017 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause
package rpc

import (
	"net"
	"os"
	"syscall"
)

func isAddrInUse(err error) bool {
	if er, ok := (err.(*net.OpError)); ok {
		if syser, ok := er.Err.(*os.SyscallError); ok {
			return syser.Err == syscall.EADDRINUSE
		}
	}
	return false
}
