package discovery

import "testing"

func TestNormalizeConsulHTTPAddr(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"http://127.0.0.1:8500", "127.0.0.1:8500"},
		{"https://consul.example:443", "consul.example:443"},
		{"127.0.0.1:8500", "127.0.0.1:8500"},
	}
	for _, tc := range tests {
		if got := NormalizeConsulHTTPAddr(tc.in); got != tc.want {
			t.Fatalf("%q: got %q want %q", tc.in, got, tc.want)
		}
	}
}
