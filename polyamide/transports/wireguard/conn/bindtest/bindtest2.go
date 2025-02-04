/* SPDX-License-Identifier: MIT
 *
 * Copyright (C) 2017-2023 WireGuard LLC. All Rights Reserved.
 */

package bindtest

import (
	"github.com/encodeous/polyamide/conn"
	"net"
	"net/netip"
)

type chanPkt struct {
	data   []byte
	remote ChannelEndpoint2
}

type ChannelBind2 struct {
	rx, tx      *chan chanPkt
	closeSignal chan bool
}

type ChannelEndpoint2 netip.AddrPort

var (
	_ conn.Bind     = (*ChannelBind2)(nil)
	_ conn.Endpoint = (*ChannelEndpoint2)(nil)
)

func NewChannelBind2() [2]conn.Bind {
	arx4 := make(chan chanPkt, 8192)
	brx4 := make(chan chanPkt, 8192)
	var binds [2]ChannelBind2
	binds[0].rx = &arx4
	binds[0].tx = &brx4

	binds[1].rx = &brx4
	binds[1].tx = &arx4

	return [2]conn.Bind{&binds[0], &binds[1]}
}

func (c ChannelEndpoint2) ClearSrc() {}

func (c ChannelEndpoint2) SrcToString() string { return "" }

func (c ChannelEndpoint2) DstToString() string {
	return netip.AddrPort(c).String()
}

func (c ChannelEndpoint2) DstToBytes() []byte {
	addr := netip.AddrPort(c)
	b, _ := addr.MarshalBinary()
	return b
}

func (c ChannelEndpoint2) DstIP() netip.Addr {
	return netip.AddrPort(c).Addr()
}

func (c ChannelEndpoint2) SrcIP() netip.Addr { return netip.Addr{} }
func (c ChannelEndpoint2) DstIPPort() netip.AddrPort {
	return netip.AddrPort(c)
}

func (c *ChannelBind2) Open(port uint16) (fns []conn.ReceiveFunc, actualPort uint16, err error) {
	c.closeSignal = make(chan bool)
	fns = append(fns, c.makeReceiveFunc(*c.rx))
	return fns, port, nil
}

func (c *ChannelBind2) Close() error {
	if c.closeSignal != nil {
		select {
		case <-c.closeSignal:
		default:
			close(c.closeSignal)
		}
	}
	return nil
}

func (c *ChannelBind2) BatchSize() int { return 1 }

func (c *ChannelBind2) SetMark(mark uint32) error { return nil }

func (c *ChannelBind2) makeReceiveFunc(ch chan chanPkt) conn.ReceiveFunc {
	return func(bufs [][]byte, sizes []int, eps []conn.Endpoint) (n int, err error) {
		select {
		case <-c.closeSignal:
			return 0, net.ErrClosed
		case rx := <-ch:
			copied := copy(bufs[0], rx.data)
			sizes[0] = copied
			eps[0] = rx.remote
			return 1, nil
		}
	}
}

func (c *ChannelBind2) Send(bufs [][]byte, ep conn.Endpoint) error {
	for _, b := range bufs {
		select {
		case <-c.closeSignal:
			return net.ErrClosed
		default:
			bc := make([]byte, len(b))
			copy(bc, b)
			*c.tx <- chanPkt{
				data:   bc,
				remote: ep.(ChannelEndpoint2),
			}
		}
	}
	return nil
}

func (c *ChannelBind2) ParseEndpoint(s string) (conn.Endpoint, error) {
	ap, err := netip.ParseAddrPort(s)
	if err != nil {
		return nil, err
	}
	return ChannelEndpoint2(ap), err
}
