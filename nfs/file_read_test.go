// Copyright © 2017 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: BSD-2-Clause
package nfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/willscott/go-nfs-client/nfs/rpc"
	"github.com/willscott/go-nfs-client/nfs/xdr"
)

// serveReadCursor replies over the real RPC transport without substituting for
// File.Read, its offset or its wire decoder.
func serveReadCursor(t *testing.T, status uint32) *File {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var accepted net.Conn
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		mu.Lock()
		accepted = conn
		mu.Unlock()
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		for {
			var marker uint32
			if err := binary.Read(conn, binary.BigEndian, &marker); err != nil {
				if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
					done <- nil
				} else {
					done <- err
				}
				return
			}
			if marker>>31 != 1 || marker&0x7fffffff > 4096 {
				done <- fmt.Errorf("unexpected READ RPC frame: %x", marker)
				return
			}
			body := make([]byte, marker&0x7fffffff)
			if _, err := io.ReadFull(conn, body); err != nil {
				done <- err
				return
			}
			var request struct {
				Xid, Type uint32
				rpc.Header
				Handle []byte
				Offset uint64
				Count  uint32
			}
			if err := xdr.Read(bytes.NewReader(body), &request); err != nil {
				done <- err
				return
			}
			if request.Proc != NFSProc3Read {
				done <- fmt.Errorf("unexpected procedure: %d", request.Proc)
				return
			}
			payload := []byte("payload")
			var data []byte
			eof := uint32(1)
			if request.Offset < uint64(len(payload)) {
				data = payload[request.Offset:]
				if uint64(request.Count) < uint64(len(data)) {
					data = data[:request.Count]
					eof = 0
				}
			}
			reply := new(bytes.Buffer)
			// Accepted RPC reply, null verifier, NFS status and absent attributes.
			for _, word := range []uint32{request.Xid, 1, 0, 0, 0, 0, status, 0} {
				if err := xdr.Write(reply, word); err != nil {
					done <- err
					return
				}
			}
			if status == 0 {
				if err := xdr.Write(reply, struct {
					Count, EOF uint32
					Data       []byte
				}{uint32(len(data)), eof, data}); err != nil {
					done <- err
					return
				}
			}
			if err := binary.Write(conn, binary.BigEndian, uint32(reply.Len())|1<<31); err != nil {
				done <- err
				return
			}
			if _, err := io.Copy(conn, reply); err != nil {
				done <- err
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		if accepted != nil {
			_ = accepted.Close()
		}
		mu.Unlock()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, net.ErrClosed) {
				t.Error(err)
			}
		case <-time.After(2 * time.Second):
			t.Error("READ reply server did not join")
		}
	})
	var client *rpc.Client
	for attempt := 0; attempt < 8; attempt++ {
		client, err = rpc.DialTCP("tcp", listener.Addr().String(), false)
		if err == nil || !errors.Is(err, syscall.EADDRINUSE) {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return &File{Target: &Target{Client: client}, fsinfo: &FSInfo{RTMax: 32769}, fh: []byte("file")}
}

func TestReadAdvancesCursorWithEOF(t *testing.T) {
	for _, prefix := range []int{0, 3} {
		t.Run(fmt.Sprintf("prefix_%d", prefix), func(t *testing.T) {
			file := serveReadCursor(t, 0)
			if prefix != 0 {
				buf := make([]byte, prefix)
				if n, err := file.Read(buf); n != prefix || err != nil || string(buf) != "pay" {
					t.Fatalf("prefix READ n=%d err=%v data=%q", n, err, buf)
				}
			}
			buf := make([]byte, 32)
			n, err := file.Read(buf)
			if n != 7-prefix || !errors.Is(err, io.EOF) || string(buf[:n]) != "payload"[prefix:] {
				t.Fatalf("terminal READ n=%d err=%v data=%q", n, err, buf[:n])
			}
			if pos, err := file.Seek(0, io.SeekCurrent); err != nil || pos != 7 {
				t.Errorf("terminal bytes must advance cursor: position=%d want=7 err=%v", pos, err)
			}
			if n, err := file.Read(buf); n != 0 || !errors.Is(err, io.EOF) {
				t.Errorf("second READ must not replay terminal bytes: n=%d err=%v", n, err)
			}
		})
	}
}

func TestReadAtDoesNotAdvanceCursor(t *testing.T) {
	file := serveReadCursor(t, 0)
	buf := make([]byte, 32)
	if n, err := file.ReadAt(buf, 0); n != 7 || !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt n=%d err=%v", n, err)
	}
	if pos, _ := file.Seek(0, io.SeekCurrent); pos != 0 {
		t.Fatalf("ReadAt moved stream position: %d", pos)
	}
	if n, err := file.Read(buf); n != 7 || !errors.Is(err, io.EOF) || string(buf[:n]) != "payload" {
		t.Fatalf("Read after ReadAt n=%d err=%v data=%q", n, err, buf[:n])
	}
}

func TestReadErrorPreservesCursorAndIdentity(t *testing.T) {
	file := serveReadCursor(t, 13) // NFS3ERR_ACCES
	buf := make([]byte, 32)
	n, err := file.Read(buf)
	var status *Error
	if n != 0 || !errors.As(err, &status) || status.ErrorNum != NFS3ErrAcces {
		t.Errorf("failed READ must preserve status identity: n=%d err=%v", n, err)
	}
	if pos, _ := file.Seek(0, io.SeekCurrent); pos != 0 {
		t.Errorf("failed READ moved position: %d", pos)
	}
}
