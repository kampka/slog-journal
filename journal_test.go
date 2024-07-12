package slogjournal

import (
	"context"
	"log/slog"
	"net"
	"os"
	"syscall"
	"testing"
)

func TestCanWriteMessageToSocket(t *testing.T) {
	tempDir, err := os.MkdirTemp(os.TempDir(), "journal")
	if err != nil {
		t.Fatal(err)
	}
	addr := tempDir + "/socket"
	raddr, err := net.ResolveUnixAddr("unixgram", addr)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.ListenUnixgram("unixgram", raddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	handler, err := NewHandler(&Options{Addr: addr})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("NormalSize", func(t *testing.T) {
		if err := handler.Handle(context.TODO(), slog.Record{Level: slog.LevelInfo, Message: "Hello, World!"}); err != nil {
			t.Fatal(err)
		}

		buf := make([]byte, 1024)
		oob := make([]byte, 1024)

		n, oobn, _, _, err := conn.ReadMsgUnix(buf, oob)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			t.Error("no data read")
		}
		if oobn != 0 {
			t.Error("did not expect oob data")
		}
	})

	t.Run("TooLarge", func(t *testing.T) {

		handler.conn.SetWriteBuffer(1024)

		largeLog := "Hello, World!"
		for range 1024 {
			largeLog += "a"
		}

		if err := handler.Handle(context.TODO(), slog.Record{Level: slog.LevelInfo, Message: largeLog}); err != nil {
			t.Fatal(err)
		}

		buf := make([]byte, 1024)
		oob := make([]byte, 1024)

		_, oobn, _, _, err := conn.ReadMsgUnix(buf, oob)
		if err != nil {
			t.Error(err)
		}

		ctrl, err := syscall.ParseSocketControlMessage(oob[:oobn])
		if err != nil {
			t.Error(err)
		}

		for _, m := range ctrl {
			rights, err := syscall.ParseUnixRights(&m)
			if err != nil {
				t.Error(err)
			}
			for _, fd := range rights {
				syscall.SetNonblock(int(fd), true)
				f := os.NewFile(uintptr(fd), "journal")
				defer f.Close()
				f.Seek(0, 0)
				buf := make([]byte, 4096)
				n, err := f.Read(buf)
				if err != nil {
					t.Fatal(err)
				}
				if n == 0 {
					t.Error("no data read")
				}
			}
		}

	})

}
