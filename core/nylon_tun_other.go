//go:build !linux

package core

// Other platforms do not coalesce packets, so host buffers start large enough
// for a packet at the common Ethernet MTU and grow when a packet is larger.
const minHostBufferCap = tunHeadroom + 1500
