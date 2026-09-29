//go:build !linux

package device

import (
	"github.com/encodeous/nylon/polyamide/transports/wireguard/conn"
	"github.com/encodeous/nylon/polyamide/transports/wireguard/rwcancel"
)

func (device *Device) startRouteListener(_ conn.Bind) (*rwcancel.RWCancel, error) {
	return nil, nil
}
