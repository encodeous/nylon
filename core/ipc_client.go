package core

import (
	"bufio"
	"fmt"

	"github.com/encodeous/nylon/polyamide/ipc"
	"github.com/encodeous/nylon/protocol"
)

func SendIPCRequest(itf string, req *protocol.IpcRequest) (*protocol.IpcResponse, error) {
	conn, err := ipc.UAPIDial(itf)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", itf, err)
	}
	defer conn.Close()

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

	if _, err := rw.WriteString("get=nylon\n"); err != nil {
		return nil, err
	}

	data, err := pjMarshal.Marshal(req)
	if err != nil {
		return nil, err
	}
	if _, err := rw.Write(data); err != nil {
		return nil, err
	}
	if _, err := rw.WriteString("\n"); err != nil {
		return nil, err
	}
	if err := rw.Flush(); err != nil {
		return nil, err
	}

	line, err := rw.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	resp := &protocol.IpcResponse{}
	if err := pjUnmarshal.Unmarshal(line, resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	return resp, nil
}

func SendIPCStream(itf string, req *protocol.IpcRequest, handler func(*protocol.IpcResponse) error) error {
	conn, err := ipc.UAPIDial(itf)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", itf, err)
	}
	defer conn.Close()

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

	if _, err := rw.WriteString("get=nylon\n"); err != nil {
		return err
	}

	data, err := pjMarshal.Marshal(req)
	if err != nil {
		return err
	}
	if _, err := rw.Write(data); err != nil {
		return err
	}
	if _, err := rw.WriteString("\n"); err != nil {
		return err
	}
	if err := rw.Flush(); err != nil {
		return err
	}

	for {
		line, err := rw.ReadBytes('\n')
		if err != nil {
			return nil // stream ended
		}
		resp := &protocol.IpcResponse{}
		if err := pjUnmarshal.Unmarshal(line, resp); err != nil {
			return fmt.Errorf("unmarshal stream response: %w", err)
		}
		if err := handler(resp); err != nil {
			return err
		}
	}
}
