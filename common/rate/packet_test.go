package rate

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/juju/ratelimit"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
)

func TestPacketRateLimitAppliesToRealUDPEcho(t *testing.T) {
	server, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go func() {
		data := make([]byte, 4096)
		for {
			n, addr, err := server.ReadFrom(data)
			if err != nil {
				return
			}
			_, _ = server.WriteTo(data[:n], addr)
		}
	}()
	raw, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	raw.SetDeadline(time.Now().Add(3 * time.Second))
	conn := NewPacketRateLimiter(bufio.NewPacketConn(raw), func() (*ratelimit.Bucket, bool) { return ratelimit.NewBucketWithRate(8192, 1), false })
	payload := bytes.Repeat([]byte("p"), 1024)
	packet := buf.New()
	packet.Write(payload)
	start := time.Now()
	if err := conn.WritePacket(packet, M.ParseSocksaddr(server.LocalAddr().String())); err != nil {
		t.Fatal(err)
	}
	response := buf.New()
	defer response.Release()
	if _, err := conn.ReadPacket(response); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, response.Bytes()) {
		t.Fatal("UDP payload mismatch")
	}
	if time.Since(start) < 200*time.Millisecond {
		t.Fatal("UDP limiter was bypassed")
	}
}
