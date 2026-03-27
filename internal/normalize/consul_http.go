package normalize

import (
	"net/url"
	"strings"
)

// ConsulHTTPAddr приводит адрес HTTP API Consul к виду host:port для клиента.
func ConsulHTTPAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if stringsHasAnyPrefix(addr, "http://", "https://") {
		u, err := url.Parse(addr)
		if err != nil {
			return addr
		}
		host := u.Hostname()
		port := u.Port()
		if port == "" {
			if u.Scheme == "https" {
				port = "443"
			} else {
				port = "80"
			}
		}
		return host + ":" + port
	}
	return addr
}

func stringsHasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
