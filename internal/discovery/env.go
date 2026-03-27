package discovery

import (
	"net"
	"os"
)

// ConsulAddrFromEnv возвращает адрес HTTP API Consul (host:port).
func ConsulAddrFromEnv() string {
	if a := os.Getenv("CONSUL_HTTP_ADDR"); a != "" {
		return NormalizeConsulHTTPAddr(a)
	}
	host := os.Getenv("CONSUL_HOST")
	port := os.Getenv("CONSUL_PORT")
	if host != "" && port != "" {
		return net.JoinHostPort(host, port)
	}
	return ""
}

// ConsulTokenFromEnv возвращает ACL token, если задан.
func ConsulTokenFromEnv() string {
	return os.Getenv("CONSUL_HTTP_TOKEN")
}
