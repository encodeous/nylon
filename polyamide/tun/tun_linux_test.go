//go:build linux

package tun

import (
	"bytes"
	"os"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

func TestNativeTunReadDrainsAvailablePackets(t *testing.T) {
	for _, packetCount := range []int{1, 3} {
		t.Run(strconv.Itoa(packetCount), func(t *testing.T) {
			fds, err := unix.Socketpair(
				unix.AF_UNIX,
				unix.SOCK_DGRAM|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC,
				0,
			)
			if err != nil {
				t.Fatal(err)
			}
			reader := os.NewFile(uintptr(fds[0]), "tun-read-test")
			t.Cleanup(func() {
				reader.Close()
				unix.Close(fds[1])
			})

			wantPackets := make([][]byte, packetCount)
			for i := range packetCount {
				wantPackets[i] = []byte{byte(i + 1), byte(i + 11)}
				if _, err := unix.Write(fds[1], wantPackets[i]); err != nil {
					t.Fatal(err)
				}
			}

			const offset = 4
			bufs := make([][]byte, 8)
			sizes := make([]int, len(bufs))
			for i := range bufs {
				bufs[i] = make([]byte, 64)
			}
			tun := &NativeTun{
				tunFile: reader,
				errors:  make(chan error),
			}

			count, err := tun.Read(bufs, sizes, offset)
			if err != nil {
				t.Fatal(err)
			}
			if count != packetCount {
				t.Fatalf("Read returned %d packets, want %d", count, packetCount)
			}
			for i, want := range wantPackets {
				if sizes[i] != len(want) {
					t.Errorf("sizes[%d] = %d, want %d", i, sizes[i], len(want))
				}
				if got := bufs[i][offset : offset+sizes[i]]; !bytes.Equal(got, want) {
					t.Errorf("packet %d = %v, want %v", i, got, want)
				}
			}
		})
	}
}

func TestNativeTunReadDefersVirtioFrameThatExceedsRemainingBatch(t *testing.T) {
	fds, err := unix.Socketpair(
		unix.AF_UNIX,
		unix.SOCK_DGRAM|unix.SOCK_NONBLOCK|unix.SOCK_CLOEXEC,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	reader := os.NewFile(uintptr(fds[0]), "tun-virtio-read-test")
	t.Cleanup(func() {
		reader.Close()
		unix.Close(fds[1])
	})

	singlePacket := udp4Packet(ip4PortA, ip4PortB, 16)
	if err := (&virtioNetHdr{}).encode(singlePacket); err != nil {
		t.Fatal(err)
	}
	segmentedPacket := udp4Packet(ip4PortA, ip4PortB, 200)
	segmentedHeader := virtioNetHdr{
		flags:      unix.VIRTIO_NET_HDR_F_NEEDS_CSUM,
		gsoType:    unix.VIRTIO_NET_HDR_GSO_UDP_L4,
		gsoSize:    100,
		hdrLen:     28,
		csumStart:  20,
		csumOffset: 6,
	}
	if err := segmentedHeader.encode(segmentedPacket); err != nil {
		t.Fatal(err)
	}
	for _, packet := range [][]byte{singlePacket, segmentedPacket} {
		if _, err := unix.Write(fds[1], packet); err != nil {
			t.Fatal(err)
		}
	}

	const outputOffset = 4
	bufs := make([][]byte, 2)
	sizes := make([]int, len(bufs))
	for i := range bufs {
		bufs[i] = make([]byte, 256)
	}
	tun := &NativeTun{
		tunFile: reader,
		errors:  make(chan error),
		vnetHdr: true,
	}

	count, err := tun.Read(bufs, sizes, outputOffset)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("first Read returned %d packets, want 1", count)
	}
	if sizes[0] != len(singlePacket)-virtioNetHdrLen {
		t.Fatalf("first packet size = %d, want %d", sizes[0], len(singlePacket)-virtioNetHdrLen)
	}

	count, err = tun.Read(bufs, sizes, outputOffset)
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("second Read returned %d packets, want 2", count)
	}
	for i, wantSize := range []int{128, 128} {
		if sizes[i] != wantSize {
			t.Errorf("sizes[%d] = %d, want %d", i, sizes[i], wantSize)
		}
	}
}
