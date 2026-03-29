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
		return normalizeNATSURL(u)
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

// normalizeNATSURL пересобирает nats://user:pass@host:port без net/url.Parse на целой строке:
// в пароле могут быть '?', ':', '@' и др.; стандартный парсер принимает '?' за начало query и ломается
// (см. лог grid-manager «invalid port … after host»).
func normalizeNATSURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	const (
		natsP = "nats://"
		tlsP  = "tls://"
	)
	var scheme string
	var rest string
	switch {
	case strings.HasPrefix(raw, natsP):
		scheme = "nats"
		rest = raw[len(natsP):]
	case strings.HasPrefix(raw, tlsP):
		scheme = "tls"
		rest = raw[len(tlsP):]
	default:
		return raw
	}
	at := strings.LastIndex(rest, "@")
	if at < 0 {
		return raw
	}
	userinfo, hostport := rest[:at], rest[at+1:]
	if hostport == "" {
		return raw
	}
	var u url.URL
	u.Scheme = scheme
	u.Host = hostport
	colon := strings.IndexByte(userinfo, ':')
	switch {
	case colon < 0:
		if userinfo != "" {
			u.User = url.User(userinfo)
		}
	case colon == 0:
		// nats://:token@host — только пароль
		u.User = url.UserPassword("", userinfo[colon+1:])
	default:
		u.User = url.UserPassword(userinfo[:colon], userinfo[colon+1:])
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
