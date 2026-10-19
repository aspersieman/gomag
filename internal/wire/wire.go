// Package wire implements the minimal subset of the Wayland wire protocol
// needed by gomag: message framing, fd passing and object dispatch.
package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
)

// Message is a decoded event received from the compositor.
type Message struct {
	Object uint32
	Opcode uint16
	Args   []byte
	Fds    []int
}

// Conn is a connection to a Wayland compositor.
type Conn struct {
	c      *net.UnixConn
	nextID uint32
	// Events delivers every event received from the compositor. It is closed
	// when the connection ends; Err then reports why.
	Events chan Message
	err    error
}

// Connect dials the compositor named by WAYLAND_DISPLAY.
func Connect() (*Conn, error) {
	name := os.Getenv("WAYLAND_DISPLAY")
	if name == "" {
		name = "wayland-0"
	}
	path := name
	if !filepath.IsAbs(name) {
		dir := os.Getenv("XDG_RUNTIME_DIR")
		if dir == "" {
			return nil, errors.New("XDG_RUNTIME_DIR is not set")
		}
		path = filepath.Join(dir, name)
	}
	uc, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	return NewConn(uc), nil
}

// NewConn wraps an existing unix connection and starts reading events.
func NewConn(uc *net.UnixConn) *Conn {
	c := &Conn{c: uc, nextID: 1, Events: make(chan Message, 256)}
	go c.readLoop()
	return c
}

// Err returns the error that terminated the event stream.
func (c *Conn) Err() error { return c.err }

// Close closes the connection.
func (c *Conn) Close() error { return c.c.Close() }

// NewID allocates a fresh client-side object id (ids are never reused).
func (c *Conn) NewID() uint32 {
	c.nextID++
	return c.nextID
}

func (c *Conn) readLoop() {
	defer close(c.Events)
	var buf []byte
	var fds []int
	tmp := make([]byte, 65536)
	oob := make([]byte, syscall.CmsgSpace(4*28))
	for {
		n, oobn, _, _, err := c.c.ReadMsgUnix(tmp, oob)
		if oobn > 0 {
			if msgs, e := syscall.ParseSocketControlMessage(oob[:oobn]); e == nil {
				for _, m := range msgs {
					if r, e := syscall.ParseUnixRights(&m); e == nil {
						fds = append(fds, r...)
					}
				}
			}
		}
		if n < 0 {
			n = 0
		}
		buf = append(buf, tmp[:n]...)
		for len(buf) >= 8 {
			id := binary.LittleEndian.Uint32(buf[0:])
			w := binary.LittleEndian.Uint32(buf[4:])
			size := int(w >> 16)
			if size < 8 || size > 1<<20 {
				c.err = fmt.Errorf("bad message size %d", size)
				return
			}
			if size > len(buf) {
				break
			}
			m := Message{Object: id, Opcode: uint16(w & 0xffff), Args: append([]byte(nil), buf[8:size]...)}
			if len(fds) > 0 {
				m.Fds, fds = fds, nil
			}
			buf = buf[size:]
			c.Events <- m
		}
		if err != nil {
			c.err = err
			return
		}
	}
}

// Request is a request under construction.
type Request struct {
	c    *Conn
	obj  uint32
	op   uint16
	body []byte
	fds  []int
}

// Req starts building a request on obj with the given opcode.
func (c *Conn) Req(obj uint32, op uint16) *Request {
	return &Request{c: c, obj: obj, op: op}
}

func (r *Request) U32(v uint32) *Request {
	r.body = binary.LittleEndian.AppendUint32(r.body, v)
	return r
}

func (r *Request) I32(v int32) *Request { return r.U32(uint32(v)) }

// Str appends a Wayland string (length-prefixed, NUL terminated, padded).
func (r *Request) Str(s string) *Request {
	r.U32(uint32(len(s) + 1))
	r.body = append(r.body, s...)
	r.body = append(r.body, 0)
	for len(r.body)%4 != 0 {
		r.body = append(r.body, 0)
	}
	return r
}

// Fd attaches a file descriptor to the message.
func (r *Request) Fd(fd int) *Request {
	r.fds = append(r.fds, fd)
	return r
}

// Send writes the request to the socket.
func (r *Request) Send() error {
	msg := make([]byte, 8, 8+len(r.body))
	binary.LittleEndian.PutUint32(msg[0:], r.obj)
	binary.LittleEndian.PutUint32(msg[4:], uint32(8+len(r.body))<<16|uint32(r.op))
	msg = append(msg, r.body...)
	var oob []byte
	if len(r.fds) > 0 {
		oob = syscall.UnixRights(r.fds...)
	}
	_, _, err := r.c.c.WriteMsgUnix(msg, oob, nil)
	return err
}

// Args decodes event arguments sequentially.
type Args struct {
	b   []byte
	Err error
}

func NewArgs(b []byte) *Args { return &Args{b: b} }

func (a *Args) U32() uint32 {
	if len(a.b) < 4 {
		a.Err = errors.New("short message")
		return 0
	}
	v := binary.LittleEndian.Uint32(a.b)
	a.b = a.b[4:]
	return v
}

func (a *Args) I32() int32 { return int32(a.U32()) }

// Fixed decodes a 24.8 fixed-point number.
func (a *Args) Fixed() float64 { return float64(a.I32()) / 256 }

func (a *Args) Str() string {
	n := int(a.U32())
	if a.Err != nil || n == 0 {
		return ""
	}
	padded := (n + 3) &^ 3
	if len(a.b) < padded {
		a.Err = errors.New("short string")
		return ""
	}
	s := string(a.b[:n-1])
	a.b = a.b[padded:]
	return s
}
