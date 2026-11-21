package mdns

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
)

func TestAdvertiserPacketAndQueryMatching(t *testing.T) {
	adv := &Advertiser{
		Port:     8080,
		Hostname: "folio.local.",
		IPs:      []net.IP{net.ParseIP("192.168.1.50")},
		TXTs:     []string{"service=folio", "path=/api/discover"},
	}

	pkt := adv.BuildAnnouncementPacket(120)
	if len(pkt) < 40 {
		t.Fatalf("expected non-empty mDNS announcement packet, got %d bytes", len(pkt))
	}
	if flags := binary.BigEndian.Uint16(pkt[2:4]); flags != 0x8400 {
		t.Fatalf("expected authoritative response flags 0x8400, got 0x%x", flags)
	}
	if !bytes.Contains(pkt, []byte("service=folio")) {
		t.Fatalf("expected TXT payload service=folio in mDNS packet")
	}

	// Build a standard PTR query for _folio._tcp.local.
	var q bytes.Buffer
	hdr := make([]byte, 12)
	binary.BigEndian.PutUint16(hdr[4:6], 1) // QDCOUNT=1
	q.Write(hdr)
	q.Write(encodeDNSName(serviceType))
	_ = binary.Write(&q, binary.BigEndian, typePTR)
	_ = binary.Write(&q, binary.BigEndian, classIN)

	if !adv.ShouldRespond(q.Bytes()) {
		t.Fatalf("expected ShouldRespond=true for _folio._tcp.local. PTR query")
	}

	// Unrelated query should not match
	var unrelated bytes.Buffer
	unrelated.Write(hdr)
	unrelated.Write(encodeDNSName("_http._tcp.local."))
	_ = binary.Write(&unrelated, binary.BigEndian, typePTR)
	_ = binary.Write(&unrelated, binary.BigEndian, classIN)
	if adv.ShouldRespond(unrelated.Bytes()) {
		t.Fatalf("expected ShouldRespond=false for unrelated _http._tcp.local. query")
	}
}
