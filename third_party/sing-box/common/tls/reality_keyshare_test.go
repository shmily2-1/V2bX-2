//go:build with_utls

package tls

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"slices"
	"testing"
	"time"

	utls "github.com/metacubex/utls"
)

func TestRealityKeySharePair(t *testing.T) {
	for _, tc := range []struct {
		name         string
		group, share bool
	}{
		{"both", true, true},
		{"group-only", true, false},
		{"share-only", false, true},
		{"neither", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			curves := &utls.SupportedCurvesExtension{Curves: []utls.CurveID{utls.X25519}}
			shares := &utls.KeyShareExtension{KeyShares: []utls.KeyShare{{Group: utls.X25519, Data: []byte("classical")}}}
			hybrid := []byte("hybrid-key-material")
			if tc.group {
				curves.Curves = append([]utls.CurveID{utls.X25519MLKEM768}, curves.Curves...)
			}
			if tc.share {
				shares.KeyShares = append([]utls.KeyShare{{Group: utls.X25519MLKEM768, Data: hybrid}}, shares.KeyShares...)
			}
			changed := normalizeRealityKeyShares([]utls.TLSExtension{curves, shares})
			if changed != (tc.group != tc.share) {
				t.Fatalf("unexpected changed=%v", changed)
			}
			wantHybrid := tc.group && tc.share
			if slices.Contains(curves.Curves, utls.X25519MLKEM768) != wantHybrid {
				t.Fatal("hybrid group does not match its key share")
			}
			wantShares := 1
			if wantHybrid {
				wantShares = 2
				if shares.KeyShares[0].Group != utls.X25519MLKEM768 || !bytes.Equal(shares.KeyShares[0].Data, hybrid) {
					t.Fatal("changed the hybrid key material or ordering")
				}
			}
			if len(shares.KeyShares) != wantShares || shares.KeyShares[wantShares-1].Group != utls.X25519 ||
				!bytes.Equal(shares.KeyShares[wantShares-1].Data, []byte("classical")) {
				t.Fatal("changed the standalone X25519 share")
			}
		})
	}
}

func TestRealityChromeKeepsHybridKeyShare(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	conn := utls.UClient(client, &utls.Config{ServerName: "localhost"}, utls.HelloChrome_Auto)
	if err := conn.BuildHandshakeState(); err != nil {
		t.Fatal(err)
	}
	if normalizeRealityKeyShares(conn.Extensions) {
		t.Fatal("chrome's complete hybrid key share should not be stripped")
	}
	var hasGroup, hasShare bool
	for _, extension := range conn.Extensions {
		switch ext := extension.(type) {
		case *utls.SupportedCurvesExtension:
			hasGroup = slices.Contains(ext.Curves, utls.X25519MLKEM768)
		case *utls.KeyShareExtension:
			for _, share := range ext.KeyShares {
				if share.Group == utls.X25519MLKEM768 {
					hasShare = len(share.Data) == 1216
				}
			}
		}
	}
	if !hasGroup || !hasShare || conn.HandshakeState.State13.KeyShareKeys.Ecdhe == nil {
		t.Fatal("chrome must keep both ML-KEM extensions and its standalone authentication key")
	}
}

// This test guards the actual ClientHandshake path, not only its normalizer.
func TestRealityClientWritesHybridHello(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	config := &RealityClientConfig{
		ctx: context.Background(),
		uClient: &UTLSClientConfig{
			ctx:    context.Background(),
			config: &utls.Config{ServerName: "localhost"},
			id:     utls.HelloChrome_Auto,
		},
		publicKey: bytes.Repeat([]byte{9}, 32),
	}
	done := make(chan error, 1)
	go func() {
		_, err := config.ClientHandshake(context.Background(), client)
		done <- err
	}()
	// Stop after observing ClientHello; certificate verification is covered by
	// the real loopback interoperability test in the root module.
	server.SetReadDeadline(time.Now().Add(5 * time.Second))
	header := make([]byte, 5)
	if _, err := io.ReadFull(server, header); err != nil {
		t.Fatal(err)
	}
	record := make([]byte, int(binary.BigEndian.Uint16(header[3:])))
	if _, err := io.ReadFull(server, record); err != nil {
		t.Fatal(err)
	}
	server.Close()
	<-done
	if header[0] != 22 || !bytes.Contains(record, []byte{0x11, 0xec, 0x04, 0xc0}) {
		t.Fatal("wire ClientHello is missing the X25519MLKEM768 key share")
	}
}
