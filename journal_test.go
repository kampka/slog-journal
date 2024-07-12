package slogjournal

import (
	"flag"
	"log/slog"
	"net"
	"os"
	"testing"
)

var (
	short = flag.Bool("short", false, "Whether to skip integration tests")
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
	if _, err := net.ListenUnixgram("unixgram", raddr); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(&Options{Addr: addr})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(handler)
	logger.Info("Hello, World!")
}

func TestCanWriteMessageToJournal(t *testing.T) {
	if *short {
		t.Skip("skipping integration test")
	}
	handler, err := NewHandler(nil)
	if err != nil {
		t.Fatal("Error creating new handler")
	}
	logger := slog.New(handler)
	logger.Info("Hello, World!")
}