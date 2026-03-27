package config

import (
	"testing"
)

func TestFromEnv_ConsulAndNATS(t *testing.T) {
	t.Setenv("CONSUL_HTTP_ADDR", "http://127.0.0.1:8500")
	t.Setenv("CONSUL_HTTP_TOKEN", "tok")
	t.Setenv("CONSUL_DNS_ADDR", "127.0.0.1:8600")
	t.Setenv("NATS_URL", "nats://127.0.0.1:4222")
	t.Setenv("DATABASE_URL_RW", "postgres://x")
	t.Setenv("REDIS_ADDR", "127.0.0.1:6379")
	t.Setenv("REDIS_PASSWORD", "secret")

	c := FromEnv()
	if c.ConsulHTTPAddr != "127.0.0.1:8500" {
		t.Fatalf("consul http: %q", c.ConsulHTTPAddr)
	}
	if c.ConsulHTTPToken != "tok" || c.ConsulDNSAddr != "127.0.0.1:8600" {
		t.Fatalf("consul: %+v", c)
	}
	if c.NATSURL != "nats://127.0.0.1:4222" {
		t.Fatalf("nats: %q", c.NATSURL)
	}
	if c.DatabaseURLRW != "postgres://x" || c.RedisAddr != "127.0.0.1:6379" || c.RedisPassword != "secret" {
		t.Fatalf("db/redis: %+v", c)
	}
}

func TestFromEnv_CellAdvertise(t *testing.T) {
	t.Run("CELL only", func(t *testing.T) {
		t.Setenv("CELL_GRPC_ENDPOINT", "cell.example:50051")
		t.Setenv("NATS_URL", "nats://127.0.0.1:4222")
		c := FromEnv()
		if c.CellGRPCAdvertise != "cell.example:50051" {
			t.Fatalf("cell advertise: %q", c.CellGRPCAdvertise)
		}
	})
	t.Run("MMO wins over CELL", func(t *testing.T) {
		t.Setenv("MMO_CELL_GRPC_ADVERTISE", "prio:50051")
		t.Setenv("CELL_GRPC_ENDPOINT", "ignored:50051")
		c := FromEnv()
		if c.CellGRPCAdvertise != "prio:50051" {
			t.Fatalf("MMO_CELL should win: %q", c.CellGRPCAdvertise)
		}
	})
}

func TestFromEnv_NATSHostPort(t *testing.T) {
	t.Setenv("NATS_URL", "")
	t.Setenv("NATS_HOST", "10.0.0.5")
	t.Setenv("NATS_PORT", "4222")
	t.Setenv("NATS_USER", "u")
	t.Setenv("NATS_PASSWORD", "pw")

	c := FromEnv()
	want := "nats://u:pw@10.0.0.5:4222"
	if c.NATSURL != want {
		t.Fatalf("nats url: got %q want %q", c.NATSURL, want)
	}
}
