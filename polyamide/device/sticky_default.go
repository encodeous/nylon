//go:build !linux

package device

import (
	"github.com/encodeous/polyamide/conn"
	"github.com/encodeous/polyamide/rwcancel"
)

func (device *Device) startRouteListener(bind conn.Bind) (*rwcancel.RWCancel, error) {
	return nil, nil
}
