package natsbus

import (
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

// Критерий Phase 0.1: два сервиса обмениваются сообщениями через NATS.
// service-a слушает grid.commands и отвечает в cell.events;
// service-b публикует команду и ожидает ack из service-a.
func TestTwoServicesExchangeViaNATS(t *testing.T) {
	port := pickFreePort(t)
	s := runNATSServer(t, port)
	defer func() {
		s.Shutdown()
		s.WaitForShutdown()
	}()
	url := fmt.Sprintf("nats://127.0.0.1:%d", port)

	// service-a (subscriber + publisher)
	serviceA, err := ConnectResilient(url, DefaultReconnectConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer serviceA.Close()

	_, err = serviceA.Subscribe(SubjectGridCommands, func(msg *nats.Msg) {
		reply := []byte("ack:" + string(msg.Data))
		_ = serviceA.Publish(SubjectCellEvents, reply)
		_ = serviceA.FlushTimeout(2 * time.Second)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := serviceA.FlushTimeout(2 * time.Second); err != nil {
		t.Fatal(err)
	}

	// service-b (publisher + subscriber)
	serviceB, err := Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer serviceB.Close()

	ackCh := make(chan string, 1)
	_, err = serviceB.Subscribe(SubjectCellEvents, func(msg *nats.Msg) {
		ackCh <- string(msg.Data)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := serviceB.FlushTimeout(2 * time.Second); err != nil {
		t.Fatal(err)
	}

	const payload = "split_prepare:cell_0_0_0"
	if err := serviceB.Publish(SubjectGridCommands, []byte(payload)); err != nil {
		t.Fatal(err)
	}
	if err := serviceB.FlushTimeout(2 * time.Second); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-ackCh:
		want := "ack:" + payload
		if got != want {
			t.Fatalf("ack=%q want=%q", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for ack from service-a")
	}
}
