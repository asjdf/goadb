package transport

import (
	"errors"
	"testing"
	"time"
)

func TestRegistryFormatsShortAndLongDeviceLists(t *testing.T) {
	registry := NewRegistry()
	registry.Upsert(Info{
		Serial:  "USB123",
		Type:    TypeUSB,
		State:   StateDevice,
		DevPath: "1-1",
		Product: "pixel",
		Model:   "Pixel_8",
		Device:  "shiba",
	})
	registry.Upsert(Info{
		Serial: "192.168.28.48:5555",
		Type:   TypeTCP,
		State:  StateOffline,
	})

	if got, want := registry.ShortList(), "192.168.28.48:5555\toffline\nUSB123\tdevice\n"; got != want {
		t.Fatalf("ShortList() = %q, want %q", got, want)
	}

	wantLong := "192.168.28.48:5555 offline transport_id:2\nUSB123 device usb:1-1 product:pixel model:Pixel_8 device:shiba transport_id:1\n"
	if got := registry.LongList(); got != wantLong {
		t.Fatalf("LongList() = %q, want %q", got, wantLong)
	}
}

func TestRegistrySelectsByAnyUSBLocalAndSerial(t *testing.T) {
	registry := NewRegistry()
	usb := registry.Upsert(Info{Serial: "USB123", Type: TypeUSB, State: StateDevice})

	if got, err := registry.Select(AnySelector()); err != nil || got.Serial != usb.Serial {
		t.Fatalf("Select(any) = %#v, %v; want %s", got, err, usb.Serial)
	}
	if got, err := registry.Select(USBSelector()); err != nil || got.Serial != usb.Serial {
		t.Fatalf("Select(usb) = %#v, %v; want %s", got, err, usb.Serial)
	}

	tcp := registry.Upsert(Info{Serial: "192.168.28.48:5555", Type: TypeTCP, State: StateDevice})
	if got, err := registry.Select(LocalSelector()); err != nil || got.Serial != tcp.Serial {
		t.Fatalf("Select(local) = %#v, %v; want %s", got, err, tcp.Serial)
	}
	if got, err := registry.Select(SerialSelector(tcp.Serial)); err != nil || got.Serial != tcp.Serial {
		t.Fatalf("Select(serial) = %#v, %v; want %s", got, err, tcp.Serial)
	}
}

func TestRegistrySelectAnyFailsWhenMoreThanOneOnlineTransportExists(t *testing.T) {
	registry := NewRegistry()
	registry.Upsert(Info{Serial: "USB123", Type: TypeUSB, State: StateDevice})
	registry.Upsert(Info{Serial: "192.168.28.48:5555", Type: TypeTCP, State: StateDevice})

	_, err := registry.Select(AnySelector())
	if !errors.Is(err, ErrAmbiguousTransport) {
		t.Fatalf("Select(any) error = %v, want ErrAmbiguousTransport", err)
	}
}

func TestRegistryRemoveDeletesTransport(t *testing.T) {
	registry := NewRegistry()
	registry.Upsert(Info{Serial: "USB123", Type: TypeUSB, State: StateDevice})

	if removed := registry.Remove("USB123"); !removed {
		t.Fatal("Remove returned false, want true")
	}
	if got := registry.ShortList(); got != "" {
		t.Fatalf("ShortList after remove = %q, want empty", got)
	}
}

func TestRegistrySubscribeReceivesInitialAndUpdatedSnapshots(t *testing.T) {
	registry := NewRegistry()
	registry.Upsert(Info{Serial: "USB123", Type: TypeUSB, State: StateDevice})

	sub := registry.Subscribe()
	defer sub.Cancel()

	initial := receiveSnapshot(t, sub.C)
	if got, want := FormatShortList(initial), "USB123\tdevice\n"; got != want {
		t.Fatalf("initial snapshot = %q, want %q", got, want)
	}

	registry.Upsert(Info{Serial: "USB123", Type: TypeUSB, State: StateOffline})
	updated := receiveSnapshot(t, sub.C)
	if got, want := FormatShortList(updated), "USB123\toffline\n"; got != want {
		t.Fatalf("updated snapshot = %q, want %q", got, want)
	}
}

func receiveSnapshot(t *testing.T, ch <-chan []Info) []Info {
	t.Helper()
	select {
	case snapshot := <-ch:
		return snapshot
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for registry snapshot")
		return nil
	}
}
