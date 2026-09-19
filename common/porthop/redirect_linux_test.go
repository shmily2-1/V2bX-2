//go:build linux

package porthop

import (
	"fmt"
	"net"
	"os"
	"testing"
	"time"
)

func TestRedirectNetNS(t *testing.T) {
	if os.Getenv("V2BX_TEST_NETNS") != "1" {
		t.Skip("requires isolated namespace opt-in")
	}
	self, err := os.Readlink("/proc/self/ns/net")
	if err != nil {
		t.Fatal(err)
	}
	init, err := os.Readlink("/proc/1/ns/net")
	if err != nil || self == init {
		t.Fatal("refusing host namespace")
	}
	for _, tc := range []struct {
		listen  string
		clients []string
	}{
		{"127.0.0.1", []string{"127.0.0.1"}},
		{"0.0.0.0", []string{"127.0.0.1"}},
		{"::1", []string{"::1"}},
		{"::", []string{"127.0.0.1", "::1"}},
	} {
		t.Run(tc.listen, func(t *testing.T) {
			network := "udp"
			if net.ParseIP(tc.listen).To4() != nil {
				network = "udp4"
			}
			conn, err := net.ListenPacket(network, net.JoinHostPort(tc.listen, "18444"))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			go func() {
				buf := make([]byte, 100)
				for {
					n, a, err := conn.ReadFrom(buf)
					if err != nil {
						return
					}
					conn.WriteTo(buf[:n], a)
				}
			}()
			c, err := Setup("netns-address-test", tc.listen, 18444, "18443-18445")
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			// A second process/tag collision must fail without deleting old rules.
			if duplicate, err := Setup("netns-address-test", tc.listen, 18444, "18443-18445"); err == nil || duplicate != nil {
				t.Fatal("existing firewall state adopted")
			}
			for _, host := range tc.clients {
				for _, port := range []int{18443, 18444, 18445} {
					sock, err := net.Dial("udp", net.JoinHostPort(host, fmt.Sprint(port)))
					if err != nil {
						t.Fatal(err)
					}
					sock.SetDeadline(time.Now().Add(time.Second))
					_, err = sock.Write([]byte("echo"))
					if err != nil {
						t.Fatal(err)
					}
					buf := make([]byte, 4)
					n, err := sock.Read(buf)
					sock.Close()
					if err != nil || n != 4 || string(buf) != "echo" {
						t.Fatalf("%s:%d echo %q err=%v", host, port, buf, err)
					}
				}
			}
			if err = c.Close(); err != nil {
				t.Fatal(err)
			}
			if err = c.Close(); err != nil {
				t.Fatal("repeat cleanup", err)
			}
		})
	}
}
