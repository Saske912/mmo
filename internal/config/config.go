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
	ConsulHTTPAddr  string
	ConsulDNSAddr   string
	ConsulHTTPToken string
	NATSURL         string
	DatabaseURLRW   string
	RedisAddr       string
	RedisPassword   string
}

// FromEnv загружает конфиг из окружения. Пустые строки означают «не задано».
func FromEnv() Config {
	c := Config{
		ConsulHTTPToken: os.Getenv("CONSUL_HTTP_TOKEN"),
		ConsulDNSAddr:   strings.TrimSpace(os.Getenv("CONSUL_DNS_ADDR")),
		DatabaseURLRW:   firstNonEmpty(os.Getenv("DATABASE_URL_RW"), os.Getenv("DATABASE_URL")),
		RedisAddr:       strings.TrimSpace(os.Getenv("REDIS_ADDR")),
		RedisPassword:   os.Getenv("REDIS_PASSWORD"),
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
