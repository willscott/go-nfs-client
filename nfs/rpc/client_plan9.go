// Copyright © 2017 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause
package rpc

func isAddrInUse(err error) bool {
	if err != nil && err.Error() == "address in use" {
		return true
	}
	return false
}
