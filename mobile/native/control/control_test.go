package control

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestPinnedEd25519Control(t *testing.T) {
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	der, e := x509.CreateCertificate(rand.Reader, &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Meldnet primary"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}, &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Meldnet primary"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}, pub, key)
	if e != nil {
		t.Fatal(e)
	}
	mode := "ok"
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS.Version != tls.VersionTLS13 {
			t.Error("TLS version")
		}
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("a", 43) {
			t.Error("credential header")
		}
		switch mode {
		case "redirect":
			http.Redirect(w, r, "https://example.invalid", 302)
		case "invalid-utf8":
			w.Write([]byte{'{', '"', 'n', '"', ':', '"', 0xff, '"', '}'})
		case "large":
			w.Write([]byte(strings.Repeat("x", 131073)))
		default:
			w.Write([]byte(`{"name":"test"}`))
		}
	}))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	sum := sha256.Sum256(der)
	u, _ := url.Parse(server.URL)
	request := Request{Origin: server.URL, Path: "/v1/network", Pin: hex.EncodeToString(sum[:]), Bearer: strings.Repeat("a", 43), Addresses: []string{u.Hostname()}, Network: 1}
	calls := 0
	bind := func(n uint64) func(string, string, syscall.RawConn) error {
		return func(_, _ string, _ syscall.RawConn) error { calls++; return nil }
	}
	invoke := func() Response { b, _ := json.Marshal(request); return Do(b, bind) }
	if r := invoke(); r.Status != 200 || string(r.Body) != `{"name":"test"}` || calls == 0 {
		t.Fatalf("pinned transport failed: status %d", r.Status)
	}
	request.Pin = strings.Repeat("0", 64)
	if invoke().Status != 0 {
		t.Fatal("accepted wrong pin")
	}
	request.Pin = hex.EncodeToString(sum[:])
	mode = "redirect"
	if invoke().Status != 302 {
		t.Fatal("redirect followed")
	}
	mode = "large"
	if invoke().Status != 0 {
		t.Fatal("oversized response accepted")
	}
	mode = "invalid-utf8"
	if invoke().Status != 0 {
		t.Fatal("invalid UTF-8 response accepted")
	}
	request.Network = 0
	if invoke().Status != 0 {
		t.Fatal("unbound network accepted")
	}
}

func TestPinnedCertificateValidityAndBinding(t *testing.T) {
	for _, test := range []struct {
		name               string
		before, after      time.Duration
		denyBinding, allow bool
	}{
		{"valid", -time.Hour, time.Hour, false, true},
		{"expired", -2 * time.Hour, -time.Hour, false, false},
		{"not-yet-valid", time.Hour, 2 * time.Hour, false, false},
		{"binding-refused", -time.Hour, time.Hour, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			pub, key, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			template := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Synthetic primary"}, NotBefore: now.Add(test.before), NotAfter: now.Add(test.after), KeyUsage: x509.KeyUsageDigitalSignature}
			der, err := x509.CreateCertificate(rand.Reader, template, template, pub, key)
			if err != nil {
				t.Fatal(err)
			}
			var received atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				received.Add(1)
				w.Write([]byte(`{"name":"test"}`))
			}))
			server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS13}
			server.StartTLS()
			defer server.Close()
			u, _ := url.Parse(server.URL)
			pin := sha256.Sum256(der)
			request := Request{Origin: server.URL, Path: "/v1/network", Pin: hex.EncodeToString(pin[:]), Bearer: strings.Repeat("a", 43), Addresses: []string{u.Hostname()}, Network: 42}
			data, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			var bindingCalls atomic.Int32
			result := Do(data, func(network uint64) func(string, string, syscall.RawConn) error {
				if network != 42 {
					t.Error("wrong network binding")
				}
				return func(_, _ string, _ syscall.RawConn) error {
					bindingCalls.Add(1)
					if test.denyBinding {
						return syscall.EPERM
					}
					return nil
				}
			})
			if bindingCalls.Load() == 0 {
				t.Fatal("test never attempted bound connection")
			}
			if test.allow {
				if result.Status != 200 || received.Load() != 1 {
					t.Fatal("valid pinned and bound connection rejected")
				}
			} else if result.Status != 0 || len(result.Body) != 0 || received.Load() != 0 {
				t.Fatal("unsafe connection returned data or sent authorization to server")
			}
		})
	}
}

func TestEncodeJNIResponsePreservesUnicodeAndEscapes(t *testing.T) {
	original := map[string]string{
		"unknown":  "emoji 😀 and é and 漢字",
		"controls": "nul\x00 newline\n tab\t quote\" backslash\\ literal \\u0041",
		"文字":       "non-ASCII object key",
	}
	body, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	encoded := Encode(Response{Status: 200, Body: body})
	if !json.Valid(encoded) {
		t.Fatal("encoder returned invalid JSON")
	}
	for _, b := range encoded {
		if b < 0x20 || b >= 0x80 {
			t.Fatal("encoder emitted raw control or non-ASCII byte")
		}
	}
	if !strings.Contains(string(encoded), `\ud83d\ude00`) || !strings.Contains(string(encoded), `\u0000`) || !strings.Contains(string(encoded), `\n`) {
		t.Fatal("missing surrogate pair or escaped controls")
	}
	var response Response
	if err = json.Unmarshal(encoded, &response); err != nil || response.Status != 200 {
		t.Fatal("cannot decode encoded response")
	}
	var decoded map[string]string
	if err = json.Unmarshal(response.Body, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(original) {
		t.Fatal("object keys changed")
	}
	for key, value := range original {
		if decoded[key] != value {
			t.Fatalf("round-trip changed %q", key)
		}
	}
	if string(Encode(Response{Status: 200, Body: json.RawMessage(`{"broken":`)})) != `{"status":0}` {
		t.Fatal("invalid response must encode as safe failure")
	}
}
