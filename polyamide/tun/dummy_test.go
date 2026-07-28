package tun

import (
	"errors"
	"os"
	"testing"
)

func TestDummyDevice(t *testing.T) {
	dev := NewDummyDevice("relay")

	name, err := dev.Name()
	if err != nil {
		t.Fatal(err)
	}
	if name != "relay" {
		t.Fatalf("Name() = %q, want relay", name)
	}
	if mtu, err := dev.MTU(); err != nil || mtu != 1420 {
		t.Fatalf("MTU() = %d, %v; want 1420, nil", mtu, err)
	}
	if n, err := dev.Write([][]byte{{1, 2, 3}}, 0); err != nil || n != 1 {
		t.Fatalf("Write() = %d, %v; want 1, nil", n, err)
	}

	readDone := make(chan error, 1)
	go func() {
		_, err := dev.Read(nil, nil, 0)
		readDone <- err
	}()

	if err := dev.Close(); err != nil {
		t.Fatal(err)
	}
	if err := dev.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-readDone; !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Read() error = %v, want os.ErrClosed", err)
	}
	if _, ok := <-dev.Events(); ok {
		t.Fatal("Events channel remained open after Close")
	}
	if _, err := dev.Write([][]byte{{1}}, 0); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Write() error = %v, want os.ErrClosed", err)
	}
}
