package session

import (
	"net/netip"
	"testing"
	"time"
)

func TestDemuxFansOutToEverySubscriber(t *testing.T) {
	tport := NewFakeTransport()
	d := NewDemux(tport)
	a := d.Subscriber()
	b := d.Subscriber()
	d.Start()

	from := netip.MustParseAddrPort("10.0.0.5:6454")
	tport.Deliver(Inbound{Data: []byte{1, 2, 3}, From: from})

	select {
	case msg := <-a.Inbound():
		if string(msg.Data) != "\x01\x02\x03" {
			t.Fatalf("subscriber a got wrong data: %v", msg.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber a never received the datagram")
	}
	select {
	case msg := <-b.Inbound():
		if string(msg.Data) != "\x01\x02\x03" {
			t.Fatalf("subscriber b got wrong data: %v", msg.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber b never received the datagram — fan-out is broken (regressed to single-reader-wins)")
	}
}

func TestDemuxTapsBothDirections(t *testing.T) {
	tport := NewFakeTransport()
	d := NewDemux(tport)
	sub := d.Subscriber()

	var sent, received int
	d.OnSend = func(data []byte, dst netip.AddrPort) { sent++ }
	d.OnReceive = func(data []byte, from netip.AddrPort) { received++ }
	d.Start()

	if err := sub.Send([]byte{9}, netip.MustParseAddrPort("10.0.0.9:6454")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := sub.Broadcast([]byte{8}); err != nil {
		t.Fatalf("Broadcast: %v", err)
	}
	if sent != 2 {
		t.Fatalf("OnSend fired %d times, want 2", sent)
	}

	tport.Deliver(Inbound{Data: []byte{1}, From: netip.MustParseAddrPort("10.0.0.5:6454")})
	<-sub.Inbound()
	if received != 1 {
		t.Fatalf("OnReceive fired %d times, want 1", received)
	}
}

func TestDemuxClosesSubscribersWhenUnderlyingCloses(t *testing.T) {
	tport := NewFakeTransport()
	d := NewDemux(tport)
	sub := d.Subscriber()
	d.Start()

	tport.Close()

	select {
	case _, ok := <-sub.Inbound():
		if ok {
			t.Fatal("expected closed channel, got a value")
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber channel never closed after underlying transport closed")
	}
}
