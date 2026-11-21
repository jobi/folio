package mdns

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os"
	"strings"
	"time"
)

const (
	mdnsAddrStr  = "224.0.0.251:5353"
	serviceType  = "_folio._tcp.local."
	dnsSDType    = "_services._dns-sd._udp.local."
	instanceName = "Folio EPUB Library._folio._tcp.local."

	typeA   uint16 = 1
	typePTR uint16 = 12
	typeTXT uint16 = 16
	typeSRV uint16 = 33
	typeANY uint16 = 255

	classIN    uint16 = 1
	classFlush uint16 = 0x8001
)

// Advertiser broadcasts and responds to RFC 6762 mDNS queries for _folio._tcp.local.
type Advertiser struct {
	Port     int
	Hostname string
	IPs      []net.IP
	TXTs     []string
}

// NewAdvertiser constructs an mDNS advertiser for the Folio HTTP port.
func NewAdvertiser(port int) *Advertiser {
	host, _ := os.Hostname()
	host = strings.TrimSpace(host)
	if host == "" {
		host = "folio"
	}
	host = strings.TrimSuffix(host, ".local")
	host = strings.ReplaceAll(host, " ", "-")
	fqdnHost := host + ".local."

	return &Advertiser{
		Port:     port,
		Hostname: fqdnHost,
		IPs:      localIPv4s(),
		TXTs: []string{
			"service=folio",
			"version=1.3.0",
			"path=/api/discover",
			fmt.Sprintf("port=%d", port),
		},
	}
}

// Start begins listening for mDNS multicast queries and sending periodic announcements until ctx is canceled.
func (a *Advertiser) Start(ctx context.Context) {
	if len(a.IPs) == 0 {
		log.Printf("mDNS: no non-loopback IPv4 interface detected; skipping _folio._tcp.local. advertisement")
		return
	}

	maddr, err := net.ResolveUDPAddr("udp4", mdnsAddrStr)
	if err != nil {
		log.Printf("mDNS: resolve multicast addr: %v", err)
		return
	}

	conn, err := net.ListenMulticastUDP("udp4", nil, maddr)
	if err != nil {
		log.Printf("mDNS: multicast listen unavailable (%v); skipping LAN Bonjour advertisement", err)
		return
	}

	log.Printf("  -> mDNS / Bonjour  : _folio._tcp.local. (host=%s, port=%d)", strings.TrimSuffix(a.Hostname, "."), a.Port)

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	// Send initial announcement burst
	announce := a.BuildAnnouncementPacket(120)
	_, _ = conn.WriteToUDP(announce, maddr)

	// Periodic re-announcement every 60 seconds
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = conn.WriteToUDP(a.BuildAnnouncementPacket(120), maddr)
			}
		}
	}()

	// Query responder loop
	go func() {
		buf := make([]byte, 4096)
		for {
			n, _, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if a.ShouldRespond(buf[:n]) {
				_, _ = conn.WriteToUDP(a.BuildAnnouncementPacket(120), maddr)
			}
		}
	}()
}

// ShouldRespond parses an incoming DNS/mDNS packet and returns true if it queries our service or host.
func (a *Advertiser) ShouldRespond(pkt []byte) bool {
	if len(pkt) < 12 {
		return false
	}
	flags := binary.BigEndian.Uint16(pkt[2:4])
	// Ignore responses (QR bit set)
	if flags&0x8000 != 0 {
		return false
	}
	qdCount := int(binary.BigEndian.Uint16(pkt[4:6]))
	if qdCount == 0 {
		return false
	}

	off := 12
	for i := 0; i < qdCount; i++ {
		qname, nextOff, ok := readDNSName(pkt, off)
		if !ok || nextOff+4 > len(pkt) {
			return false
		}
		qtype := binary.BigEndian.Uint16(pkt[nextOff : nextOff+2])
		off = nextOff + 4

		lower := strings.ToLower(qname)
		if lower == strings.ToLower(serviceType) && (qtype == typePTR || qtype == typeANY) {
			return true
		}
		if lower == strings.ToLower(dnsSDType) && (qtype == typePTR || qtype == typeANY) {
			return true
		}
		if lower == strings.ToLower(instanceName) && (qtype == typeSRV || qtype == typeTXT || qtype == typeANY) {
			return true
		}
		if lower == strings.ToLower(a.Hostname) && (qtype == typeA || qtype == typeANY) {
			return true
		}
	}
	return false
}

// BuildAnnouncementPacket builds an mDNS DNS-SD response packet containing PTR, SRV, TXT, and A records.
func (a *Advertiser) BuildAnnouncementPacket(ttl uint32) []byte {
	var buf bytes.Buffer

	// Header: ID=0, Flags=0x8400 (Response, Authoritative), QD=0, AN=count, NS=0, AR=0
	anCount := uint16(4 + len(a.IPs))
	hdr := make([]byte, 12)
	binary.BigEndian.PutUint16(hdr[2:4], 0x8400)
	binary.BigEndian.PutUint16(hdr[6:8], anCount)
	buf.Write(hdr)

	// 1. DNS-SD meta PTR: _services._dns-sd._udp.local. -> _folio._tcp.local.
	writeRR(&buf, dnsSDType, typePTR, classIN, ttl, encodeDNSName(serviceType))

	// 2. Service PTR: _folio._tcp.local. -> Folio EPUB Library._folio._tcp.local.
	writeRR(&buf, serviceType, typePTR, classIN, ttl, encodeDNSName(instanceName))

	// 3. SRV: Folio EPUB Library._folio._tcp.local. -> port, target=a.Hostname
	var srvData bytes.Buffer
	_ = binary.Write(&srvData, binary.BigEndian, uint16(0)) // Priority
	_ = binary.Write(&srvData, binary.BigEndian, uint16(0)) // Weight
	_ = binary.Write(&srvData, binary.BigEndian, uint16(a.Port))
	srvData.Write(encodeDNSName(a.Hostname))
	writeRR(&buf, instanceName, typeSRV, classFlush, ttl, srvData.Bytes())

	// 4. TXT: Folio EPUB Library._folio._tcp.local.
	var txtData bytes.Buffer
	for _, s := range a.TXTs {
		if len(s) > 255 {
			s = s[:255]
		}
		txtData.WriteByte(byte(len(s)))
		txtData.WriteString(s)
	}
	writeRR(&buf, instanceName, typeTXT, classFlush, ttl, txtData.Bytes())

	// 5. A records: <host>.local. -> IPv4
	for _, ip := range a.IPs {
		ip4 := ip.To4()
		if ip4 == nil {
			continue
		}
		writeRR(&buf, a.Hostname, typeA, classFlush, ttl, ip4)
	}

	return buf.Bytes()
}

func writeRR(buf *bytes.Buffer, name string, rtype, rclass uint16, ttl uint32, rdata []byte) {
	buf.Write(encodeDNSName(name))
	_ = binary.Write(buf, binary.BigEndian, rtype)
	_ = binary.Write(buf, binary.BigEndian, rclass)
	_ = binary.Write(buf, binary.BigEndian, ttl)
	_ = binary.Write(buf, binary.BigEndian, uint16(len(rdata)))
	buf.Write(rdata)
}

func encodeDNSName(name string) []byte {
	var out bytes.Buffer
	trimmed := strings.TrimSuffix(name, ".")
	if trimmed != "" {
		for _, label := range strings.Split(trimmed, ".") {
			if len(label) > 63 {
				label = label[:63]
			}
			out.WriteByte(byte(len(label)))
			out.WriteString(label)
		}
	}
	out.WriteByte(0)
	return out.Bytes()
}

func readDNSName(pkt []byte, offset int) (string, int, bool) {
	var labels []string
	cur := offset
	jumped := false
	endOff := offset
	seen := 0

	for cur < len(pkt) && seen < 128 {
		seen++
		l := int(pkt[cur])
		if l == 0 {
			if !jumped {
				endOff = cur + 1
			}
			return strings.Join(labels, ".") + ".", endOff, true
		}
		if l&0xC0 == 0xC0 {
			if cur+1 >= len(pkt) {
				return "", 0, false
			}
			ptr := int(binary.BigEndian.Uint16(pkt[cur:cur+2]) & 0x3FFF)
			if !jumped {
				endOff = cur + 2
				jumped = true
			}
			cur = ptr
			continue
		}
		cur++
		if cur+l > len(pkt) {
			return "", 0, false
		}
		labels = append(labels, string(pkt[cur:cur+l]))
		cur += l
	}
	return "", 0, false
}

func localIPv4s() []net.IP {
	var ips []net.IP
	ifaces, err := net.Interfaces()
	if err != nil {
		return ips
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipNet.IP.To4()
			if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
				continue
			}
			ips = append(ips, ip4)
		}
	}
	return ips
}
