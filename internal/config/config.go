package config

import (
	"net"
	"net/url"
	"os"
	"strings"

	"mmo/internal/normalize"
)

// Config централизует переменные окружения инфраструктуры (см. mmo_remote_state.tf.example).
type Config struct {
	// GatewaySkipDBMigrations: при true gateway не вызывает goose Up (миграции — только Job /migrate).
	GatewaySkipDBMigrations bool
	ConsulHTTPAddr          string
	ConsulDNSAddr           string
	ConsulHTTPToken         string
	NATSURL                 string
	DatabaseURLRW           string
	RedisAddr               string
	RedisPassword           string
	// Harbor (проброс из секрета K8s / remote state, для docker login и отладки).
	HarborRegistry string
	HarborUser     string
	HarborPassword string
	// Публичный gRPC соты для Consul (K8s DNS); см. cmd/cell-node.
	CellGRPCAdvertise string
}

// FromEnv загружает конфиг из окружения. Пустые строки означают «не задано».
func FromEnv() Config {
	skipMig := strings.EqualFold(strings.TrimSpace(os.Getenv("GATEWAY_SKIP_DB_MIGRATIONS")), "1") ||
		strings.EqualFold(strings.TrimSpace(os.Getenv("GATEWAY_SKIP_DB_MIGRATIONS")), "true")
	c := Config{
		GatewaySkipDBMigrations: skipMig,
		ConsulHTTPToken:         os.Getenv("CONSUL_HTTP_TOKEN"),
		ConsulDNSAddr:           strings.TrimSpace(os.Getenv("CONSUL_DNS_ADDR")),
		DatabaseURLRW:           firstNonEmpty(os.Getenv("DATABASE_URL_RW"), os.Getenv("DATABASE_URL")),
		RedisAddr:               strings.TrimSpace(os.Getenv("REDIS_ADDR")),
		RedisPassword:           os.Getenv("REDIS_PASSWORD"),
		HarborRegistry:          strings.TrimSpace(os.Getenv("HARBOR_REGISTRY")),
		HarborUser:              os.Getenv("HARBOR_USER"),
		HarborPassword:          os.Getenv("HARBOR_PASSWORD"),
		CellGRPCAdvertise:       firstNonEmpty(os.Getenv("MMO_CELL_GRPC_ADVERTISE"), os.Getenv("CELL_GRPC_ENDPOINT")),
	}
	if a := strings.TrimSpace(os.Getenv("CONSUL_HTTP_ADDR")); a != "" {
		c.ConsulHTTPAddr = normalize.ConsulHTTPAddr(a)
	} else {
		h, p := os.Getenv("CONSUL_HOST"), os.Getenv("CONSUL_PORT")
		if h != "" && p != "" {
			c.ConsulHTTPAddr = net.JoinHostPort(h, p)
		}
	}
	c.NATSURL = natsURLFromEnv()
	return c
}

func natsURLFromEnv() string {
	if u := strings.TrimSpace(os.Getenv("NATS_URL")); u != "" {
		return u
	}
	host, port := os.Getenv("NATS_HOST"), os.Getenv("NATS_PORT")
	if host == "" || port == "" {
		return ""
	}
	user, pass := os.Getenv("NATS_USER"), os.Getenv("NATS_PASSWORD")
	addr := net.JoinHostPort(host, port)
	u := &url.URL{Scheme: "nats", Host: addr}
	if user != "" {
		if pass != "" {
			u.User = url.UserPassword(user, pass)
		} else {
			u.User = url.User(user)
		}
	}
	return u.String()
}

func firstNonEmpty(a, b string) string {
	a = strings.TrimSpace(a)
	if a != "" {
		return a
	}
	return strings.TrimSpace(b)
}
