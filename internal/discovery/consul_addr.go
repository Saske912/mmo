package discovery

import (
	"net/url"
	"strings"
)

// NormalizeConsulHTTPAddr приводит адрес к виду host:port для api.Client.
func NormalizeConsulHTTPAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
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
