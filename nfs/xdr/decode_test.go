// Copyright © 2017 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause
package xdr

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"testing"

	"github.com/willscott/go-nfs-client/nfs/util"
)

func TestRead(t *testing.T) {
	type X struct {
		A, B, C, D uint32
	}
	x := new(X)
	b := []byte{
		0, 0, 0, 1,
		0, 0, 0, 2,
		0, 0, 0, 3,
		0, 0, 0, 4,
		1,
	}
	buf := bytes.NewBuffer(b)
	Read(buf, x)
}

// maxReadSizeReader is an io.Reader wrapper that bounds the size of each Read
// call to at most n bytes.
type maxReadSizeReader struct {
	n int
	r io.Reader
}

func (m maxReadSizeReader) Read(p []byte) (int, error) {
	if len(p) > m.n {
		p = p[:m.n]
	}
	return m.r.Read(p)
}

// TestReadOpaque verifies that ReadOpaque can read data correctly even when
// the underlying reader returns less data than requested in a single io.Read
// call. Previously the implementation of ReadOpaque assumed that io.Read would
// act like io.ReadFull, which is not guaranteed by the io.Reader interface,
// and that resulted in sporadic failures at load with short reads, depending on how
// packets arrived and got buffered.
func TestReadOpaque(t *testing.T) {
	const nfsHandleHex = `
	   00 00 00 40 90 14 1d 45 07 09 36 73 c9 96 8c 51
   d9 71 07 37 34 ae 9b 4b 98 08 77 b6 de e6 5f 3e
   e6 43 57 b0 cb b9 a2 35 56 35 4a f4 6d 38 45 f9
   eb 1f 62 1d c5 7f 72 ac 79 dc b2 50 8f 5a 4e 08
   8b f9 f2 37
`
	data, err := hex.DecodeString(regexp.MustCompile(`\s+`).ReplaceAllString(nfsHandleHex, ""))
	if err != nil {
		t.Fatalf("failed to decode hex: %v", err)
	}

	for i := 1; i <= len(data); i++ {
		t.Run(fmt.Sprintf("maxReadSize=%d", i), func(t *testing.T) {
			r := maxReadSizeReader{n: i, r: bytes.NewReader(data)}
			got, err := ReadOpaque(r)
			if err != nil {
				t.Fatalf("failed to read opaque: %v", err)
			}
			const wantHex = "90141d4507093673c9968c51d971073734ae9b4b980877b6dee65f3ee64357b0cbb9a23556354af46d3845f9eb1f621dc57f72ac79dcb2508f5a4e088bf9f237"
			gotHex := hex.EncodeToString(got)
			if gotHex != wantHex {
				t.Fatalf("opaque data mismatch:\n got:  %s\n want: %s", gotHex, wantHex)
			}
		})
	}
}

func TestByteSlice(t *testing.T) {
	util.DefaultLogger.SetDebug(true)

	// byte slices have a length field up front, followed by the data.  The
	// data is aligned to 4B.
	type ByteSlice struct {
		Length uint32
		Data   []byte
		Pad    []byte
	}

	in := &ByteSlice{
		Length: 6,
		Data:   []byte{0x0, 0x1, 0x2, 0x3, 0x4, 0x5},
		Pad:    []byte{0x0, 0x0},
	}

	b := &bytes.Buffer{}
	binary.Write(b, binary.BigEndian, uint32(in.Length))
	b.Write(in.Data)
	b.Write(in.Pad)

	var out []byte
	if err := Read(b, &out); err != nil {
		t.Log("fail in read")
		t.Fail()
		return
	}

	if len(out) != int(in.Length) {
		t.Logf("legth mismatch, expected %d, actual %d", in.Length, len(out))
		t.Fail()
		return
	}

	if bytes.Compare(in.Data, out) != 0 {
		t.FailNow()
	}
}
