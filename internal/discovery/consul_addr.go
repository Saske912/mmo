package discovery

import "mmo/internal/normalize"

// NormalizeConsulHTTPAddr приводит адрес к виду host:port для api.Client.
func NormalizeConsulHTTPAddr(addr string) string {
	return normalize.ConsulHTTPAddr(addr)
}
