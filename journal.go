package slogjournal

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"os"
	"runtime"
	"strconv"
	"syscall"
)

type Priority int

// TODO: Make private
const (
	PriEmerg Priority = iota
	PriAlert
	PriCrit
	PriErr
	PriWarning
	PriNotice
	PriInfo
	PriDebug
)

const (
	LevelNotice    slog.Level = 1
	LevelCritical  slog.Level = slog.LevelError + 1
	LevelAlert     slog.Level = slog.LevelError + 2
	LevelEmergency slog.Level = slog.LevelError + 3
)

func levelToPriority(l slog.Level) Priority {
	switch l {
	case slog.LevelDebug:
		return PriDebug
	case slog.LevelInfo:
		return PriInfo
	case LevelNotice:
		return PriNotice
	case slog.LevelWarn:
		return PriWarning
	case slog.LevelError:
		return PriErr
	case LevelCritical:
		return PriCrit
	case LevelAlert:
		return PriAlert
	default:
		panic("unreachable")
	}
}

type Options struct {
	Level slog.Leveler
	Addr  string // Address of the journal socket. If not set defaults to /run/systemd/journal/socket. This is useful for testing.
}

type Handler struct {
	opts         Options
	conn         *net.UnixConn
	addr         *net.UnixAddr
	prefix       string
	preformatted *bytes.Buffer
}

const sndBufSize = 8 * 1024 * 1024

func NewHandler(opts *Options) (*Handler, error) {
	h := &Handler{}

	if opts != nil {
		h.opts = *opts
	}

	if h.opts.Level == nil {
		h.opts.Level = slog.LevelInfo
	}
	localAddr, err := net.ResolveUnixAddr("unixgram", "")
	if err != nil {
		return nil, err
	}

	conn, err := net.ListenUnixgram("unixgram", localAddr)
	if err != nil {
		return nil, err
	}
	if err := conn.SetWriteBuffer(sndBufSize); err != nil {
		return nil, err
	}

	if h.opts.Addr == "" {
		h.opts.Addr = "/run/systemd/journal/socket"
	}
	addr, err := net.ResolveUnixAddr("unixgram", h.opts.Addr)
	if err != nil {
		return nil, err
	}

	h.conn = conn
	h.addr = addr

	h.preformatted = new(bytes.Buffer)
	h.prefix = ""

	return h, nil

}

// Enabled implements slog.Handler.
func (h *Handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.opts.Level.Level()
}

// Handle implements slog.Handler.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	buf := new(bytes.Buffer)
	h.appendKV(buf, "MESSAGE", []byte(r.Message))
	h.appendKV(buf, "PRIORITY", []byte(strconv.Itoa(int(levelToPriority(r.Level)))))
	if r.PC != 0 {
		fs := runtime.CallersFrames([]uintptr{r.PC})
		f, _ := fs.Next()
		h.appendKV(buf, "CODE_FILE", []byte(f.File))
		h.appendKV(buf, "CODE_FUNC", []byte(f.Function))
		h.appendKV(buf, "CODE_LINE", []byte(strconv.Itoa(f.Line)))
	}

	if _, err := buf.ReadFrom(h.preformatted); err != nil {
		return err
	}

	r.Attrs(func(a slog.Attr) bool {
		h.appendAttr(buf, h.prefix, a)
		return true
	})

	// NOTE: No mutex needed. datagram socket writes are atomic
	_, err := h.conn.WriteToUnix(buf.Bytes(), h.addr)
	if err == nil {
		return nil
	}
	opErr, ok := err.(*net.OpError)
	if !ok {
		return err
	}
	errno, ok := opErr.Err.(*os.SyscallError)
	if !ok {
		return err
	}
	if errno.Err == syscall.ENOENT {
		return nil
	}
	if errno.Err != syscall.ENOBUFS && errno.Err != syscall.EMSGSIZE {
		return err
	}

	file, err := tempFd()
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := io.Copy(file, buf); err != nil {
		return err
	}
	if _, _, err := h.conn.WriteMsgUnix([]byte{}, syscall.UnixRights(int(file.Fd())), h.addr); err != nil {
		return err
	}
	return nil
}

func (h *Handler) appendKV(b *bytes.Buffer, k string, v []byte) {
	if bytes.IndexByte(v, '\n') != -1 {
		b.WriteString(k)
		b.WriteByte('\n')
		binary.Write(b, binary.LittleEndian, uint64(len(v)))
		b.Write(v)
	} else {
		b.WriteString(k)
		b.WriteByte('=')
		b.Write(v)
		b.WriteByte('\n')
	}
}

func (h *Handler) appendAttr(b *bytes.Buffer, prefix string, a slog.Attr) {
	if a.Value.Kind() == slog.KindGroup {
		if a.Key != "" {
			prefix += a.Key + "_"
		}
		for _, g := range a.Value.Group() {
			h.appendAttr(b, prefix, g)
		}
	} else if key := a.Key; key != "" {
		h.appendKV(b, prefix+"_"+key, []byte(a.Value.String()))
	}
}

// WithAttrs implements slog.Handler.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	buf := new(bytes.Buffer)
	buf.ReadFrom(h.preformatted)
	// TODO: Is it correct to clobber the preformatted buffer?
	// NO
	for _, a := range attrs {
		h.appendAttr(buf, h.prefix, a)
	}
	return &Handler{
		opts:         h.opts,
		conn:         h.conn,
		addr:         h.addr,
		prefix:       h.prefix,
		preformatted: buf,
	}
}

// WithGroup implements slog.Handler.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{
		opts:         h.opts,
		conn:         h.conn,
		addr:         h.addr,
		prefix:       h.prefix + name + "_",
		preformatted: h.preformatted,
	}
}

var _ slog.Handler = &Handler{}