//go:build linux && sing && xray && hysteria2 && with_quic && with_wireguard

package core_test

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/shmily2-1/V2bX-2/conf"
	"github.com/shmily2-1/V2bX-2/localservice"
)

func TestLocalTUNAutoRouteCleanup(t *testing.T) {
	if os.Getenv("V2BX_ISOLATED_TEST") != "1" {
		t.Skip("requires isolated Linux network namespace")
	}
	before, err := exec.Command("ip", "-j", "rule", "show").Output()
	if err != nil {
		t.Fatal(err)
	}
	spec := conf.LocalService{ID: "tun-auto", Type: "tun", TUN: &conf.TUNService{Interface: "v2bxauto", Address: []string{"10.199.1.1/30"}, MTU: 1400, AutoRoute: true, RouteAddress: []string{"203.0.113.0/24"}, OutboundInterface: "testnet"}}
	instance, err := localservice.Open(spec, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = instance.Health(); err != nil {
		instance.Close()
		t.Fatal(err)
	}
	if err = instance.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := exec.Command("ip", "-j", "rule", "show").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSpace(before), bytes.TrimSpace(after)) {
		t.Fatal("TUN policy rules were not restored after shutdown")
	}
	if _, err = net.InterfaceByName("v2bxauto"); err == nil {
		t.Fatal("TUN interface survived shutdown")
	}
}

func TestLocalTUNDataPlane(t *testing.T) {
	if os.Getenv("V2BX_ISOLATED_TEST") != "1" {
		t.Skip("requires isolated Linux network/mount namespaces")
	}
	ip := func(args ...string) {
		t.Helper()
		if output, err := exec.Command("ip", args...).CombinedOutput(); err != nil {
			t.Fatalf("test network setup failed: %s (%v)", output, err)
		}
	}
	ns := fmt.Sprintf("v2bx-tun-%d", os.Getpid())
	ip("netns", "add", ns)
	t.Cleanup(func() { exec.Command("ip", "netns", "del", ns).Run() })
	ip("link", "add", "tunouter", "type", "veth", "peer", "name", "tunpeer")
	ip("link", "set", "tunpeer", "netns", ns)
	ip("address", "add", "198.20.0.1/30", "dev", "tunouter")
	ip("link", "set", "tunouter", "up")
	ip("-n", ns, "link", "set", "lo", "up")
	ip("-n", ns, "address", "add", "198.20.0.2/30", "dev", "tunpeer")
	ip("-n", ns, "link", "set", "tunpeer", "up")
	program := "import socket,threading,time\n" +
		"def tcp():\n s=socket.socket();s.bind(('198.20.0.2',19379));s.listen();print('ready',flush=True)\n while True:\n  c,_=s.accept()\n  while True:\n   data=c.recv(65536)\n   if not data: break\n   c.sendall(data)\n  c.close()\n" +
		"def udp():\n s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.bind(('198.20.0.2',19379))\n while True:\n  data,p=s.recvfrom(65536);s.sendto(data,p)\n" +
		"threading.Thread(target=tcp,daemon=True).start();threading.Thread(target=udp,daemon=True).start();time.sleep(60)"
	process := exec.Command("ip", "netns", "exec", ns, "python3", "-u", "-c", program)
	output, err := process.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { process.Process.Kill(); process.Wait() })
	reader := bufio.NewReader(output)
	if _, err = reader.ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	spec := conf.LocalService{ID: "tun-forwarding", Type: "tun", TUN: &conf.TUNService{Interface: "v2bxtun", Address: []string{"10.198.0.1/30"}, MTU: 1400, OutboundInterface: "tunouter"}}
	instance, err := localservice.Open(spec, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { instance.Close() })
	ip("route", "add", "198.20.0.2/32", "dev", "v2bxtun")
	c, err := net.DialTimeout("tcp", "198.20.0.2:19379", 3*time.Second)
	if err != nil {
		t.Fatal("TUN TCP:", err)
	}
	if err = echoPayload(c); err != nil {
		c.Close()
		t.Fatal(err)
	}
	c.Close()
	u, err := net.DialTimeout("udp", "198.20.0.2:19379", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer u.Close()
	u.SetDeadline(time.Now().Add(3 * time.Second))
	payload := bytes.Repeat([]byte("actual-tun-udp;"), 32)
	if _, err = u.Write(payload); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err = u.Read(got); err != nil || !bytes.Equal(got, payload) {
		t.Fatal("TUN UDP:", err)
	}
	traffic, err := instance.Traffic()
	if err != nil || len(traffic) != 1 || traffic[0].Upload == 0 || traffic[0].Download == 0 {
		t.Fatalf("TUN service traffic missing: %+v %v", traffic, err)
	}
}
