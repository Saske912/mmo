package web3ingest

import (
	"net/http"
	"testing"
)

func TestCheckIngestAuthOpen(t *testing.T) {
	if err := CheckIngestAuth(http.Header{}, []byte(`{}`), "", ""); err != nil {
		t.Fatal(err)
	}
}

func TestCheckIngestAuthAPIKey(t *testing.T) {
	h := http.Header{}
	h.Set(headerIngestKey, "secret-key")
	if err := CheckIngestAuth(h, []byte(`{}`), "secret-key", ""); err != nil {
		t.Fatal(err)
	}
	if err := CheckIngestAuth(h, []byte(`{}`), "wrong", ""); err == nil {
		t.Fatal("expected error")
	}
}

func TestCheckIngestAuthBearer(t *testing.T) {
	h := http.Header{}
	h.Set(headerAuthorization, "Bearer tok")
	if err := CheckIngestAuth(h, []byte(`{}`), "tok", ""); err != nil {
		t.Fatal(err)
	}
}

func TestCheckIngestAuthHMAC(t *testing.T) {
	body := []byte(`{"chain_id":1,"events":[]}`)
	h := http.Header{}
	h.Set(headerIngestSignature, ComputeHMACSignatureHex("hm", body))
	if err := CheckIngestAuth(h, body, "", "hm"); err != nil {
		t.Fatal(err)
	}
	h2 := http.Header{}
	h2.Set(headerIngestSignature, "sha256="+ComputeHMACSignatureHex("hm", body))
	if err := CheckIngestAuth(h2, body, "", "hm"); err != nil {
		t.Fatal(err)
	}
	if err := CheckIngestAuth(http.Header{}, body, "", "hm"); err == nil {
		t.Fatal("expected error")
	}
}

func TestCheckIngestAuthBoth(t *testing.T) {
	body := []byte(`{}`)
	h := http.Header{}
	h.Set(headerIngestKey, "k")
	h.Set(headerIngestSignature, ComputeHMACSignatureHex("hm", body))
	if err := CheckIngestAuth(h, body, "k", "hm"); err != nil {
		t.Fatal(err)
	}
}
