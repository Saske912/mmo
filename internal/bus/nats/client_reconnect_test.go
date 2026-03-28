package natsbus

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

func TestConnectResilientReconnectsAfterServerRestart(t *testing.T) {
	port := pickFreePort(t)
	s1 := runNATSServer(t, port)

	cfg := DefaultReconnectConfig()
	cfg.ReconnectWait = 100 * time.Millisecond
	cfg.ConnectTimeout = 500 * time.Millisecond
	c, err := ConnectResilient(fmt.Sprintf("nats://127.0.0.1:%d", port), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	received := make(chan string, 4)
	_, err = c.Conn().Subscribe(SubjectCellEvents, func(msg *nats.Msg) {
		received <- string(msg.Data)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.FlushTimeout(2 * time.Second); err != nil {
		t.Fatal(err)
	}

	pub, err := Connect(fmt.Sprintf("nats://127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	if err := pub.Publish(SubjectCellEvents, []byte("before")); err != nil {
		t.Fatal(err)
	}
	if err := pub.FlushTimeout(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	waitMsg(t, received, "before")

	s1.Shutdown()
	s1.WaitForShutdown()
	time.Sleep(200 * time.Millisecond)

	s2 := runNATSServer(t, port)
	defer func() {
		s2.Shutdown()
		s2.WaitForShutdown()
	}()

	// Даём клиенту переподключиться.
	deadline := time.After(5 * time.Second)
	for c.Conn().Status() != nats.CONNECTED {
		select {
		case <-deadline:
			t.Fatalf("client did not reconnect: %s", c.ReconnectStats())
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	if err := pub.Publish(SubjectCellEvents, []byte("after")); err == nil {
		// старый publisher мог не пережить рестарт; ignore
	}
	pub.Close()
	pub2, err := Connect(fmt.Sprintf("nats://127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer pub2.Close()
	if err := pub2.Publish(SubjectCellEvents, []byte("after")); err != nil {
		t.Fatal(err)
	}
	if err := pub2.FlushTimeout(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	waitMsg(t, received, "after")
}

func waitMsg(t *testing.T, ch <-chan string, want string) {
	t.Helper()
	select {
	case got := <-ch:
		if got != want {
			t.Fatalf("message = %q, want %q", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for message %q", want)
	}
}

func runNATSServer(t *testing.T, port int) *server.Server {
	t.Helper()
	s, err := server.NewServer(&server.Options{
		Host:   "127.0.0.1",
		Port:   port,
		NoSigs: true,
		NoLog:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go s.Start()
	if !s.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server is not ready")
	}
	return s
}

func pickFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}
