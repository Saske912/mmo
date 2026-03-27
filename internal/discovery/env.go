package discovery

import "mmo/internal/config"

// ConsulAddrFromEnv возвращает адрес HTTP API Consul (host:port).
func ConsulAddrFromEnv() string {
	return config.FromEnv().ConsulHTTPAddr
}

// ConsulTokenFromEnv возвращает ACL token, если задан.
func ConsulTokenFromEnv() string {
	return config.FromEnv().ConsulHTTPToken
}
