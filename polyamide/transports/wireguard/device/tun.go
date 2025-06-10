/* SPDX-License-Identifier: MIT
 *
 * Copyright (C) 2017-2023 WireGuard LLC. All Rights Reserved.
 */

package device

import (
	"fmt"

	"github.com/encodeous/nylon/polyamide/tun"
)

const DefaultMTU = 1420

func (device *Device) RoutineTUNEventReader() {
	device.Log.Verbosef("Routine: event worker - started")

	for event := range device.tun.device.Events() {
		if event&tun.EventMTUUpdate != 0 {
			mtu, err := device.tun.device.MTU()
			if err != nil {
				device.Log.Errorf("Failed to load updated MTU of device: %v", err)
				continue
			}
			if mtu < 0 {
				device.Log.Errorf("MTU not updated to negative value: %v", mtu)
				continue
			}
			var tooLarge string
			if mtu > MaxContentSize {
				tooLarge = fmt.Sprintf(" (too large, capped at %v)", MaxContentSize)
				mtu = MaxContentSize
			}
			old := device.tun.mtu.Swap(int32(mtu))
			if int(old) != mtu {
				device.Log.Verbosef("MTU updated: %v%s", mtu, tooLarge)
			}
		}

		if event&tun.EventUp != 0 {
			device.Log.Verbosef("Interface up requested")
			device.Up()
		}

		if event&tun.EventDown != 0 {
			device.Log.Verbosef("Interface down requested")
			device.Down()
		}
	}

	device.Log.Verbosef("Routine: event worker - stopped")
}
