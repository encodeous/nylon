package ipc

import (
	"bufio"
	"fmt"
	"net"

	"github.com/encodeous/nylon/protocol"
)

// Client is a connection to a nylon daemon. It sends one request at a time; the protocol allows more, but the
// CLI never needs them.
type Client struct {
	conn   net.Conn
	r      *bufio.Reader
	nextID uint32
}

// Dial connects to the daemon serving the nylon interface itf.
func Dial(itf string) (*Client, error) {
	conn, err := dial(itf)
	if err != nil {
		return nil, fmt.Errorf("connect to nylon on %s: %w", itf, err)
	}
	c, err := NewClient(conn)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return c, nil
}

// NewClient says hello over conn. The client owns conn from then on.
func NewClient(conn net.Conn) (*Client, error) {
	if err := WriteHello(conn); err != nil {
		return nil, err
	}
	r := bufio.NewReader(conn)
	version, err := ReadHello(r)
	if err != nil {
		return nil, fmt.Errorf("read hello: %w", err)
	}
	if version != Version {
		return nil, ErrVersion{Local: Version, Remote: version}
	}
	return &Client{conn: conn, r: r}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

// Do sends req and returns its response. A response carrying an error comes back as *Error.
func (c *Client) Do(req *protocol.IpcRequest) (*protocol.IpcResponse, error) {
	var resp *protocol.IpcResponse
	err := c.Stream(req, func(r *protocol.IpcResponse) bool {
		resp = r
		return false
	})
	return resp, err
}

// Stream sends req and passes each response to fn until fn returns false, the daemon reports an error, or the
// connection closes. Stopping early leaves the request running; close the client to end it.
func (c *Client) Stream(req *protocol.IpcRequest, fn func(*protocol.IpcResponse) bool) error {
	c.nextID++
	req.Id = c.nextID
	if err := WriteFrame(c.conn, req); err != nil {
		return err
	}
	for {
		resp := &protocol.IpcResponse{}
		if err := ReadFrame(c.r, resp); err != nil {
			return err
		}
		if resp.Id != req.Id {
			continue // a response to a request this client gave up on
		}
		if e := resp.GetError(); e != nil {
			return &Error{Code: e.Code, Message: e.Message}
		}
		if !fn(resp) {
			return nil
		}
	}
}
