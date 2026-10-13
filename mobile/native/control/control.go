// Package control implements the primary's pinned TLS 1.3 transport using Go's
// maintained TLS stack, including Ed25519 certificates on Android.
package control

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

type Request struct {
	Origin    string          `json:"origin"`
	Path      string          `json:"path"`
	Pin       string          `json:"pin"`
	Bearer    string          `json:"bearer"`
	Body      json.RawMessage `json:"body,omitempty"`
	Addresses []string        `json:"addresses"`
	Network   uint64          `json:"networkHandle"`
}
type Response struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body,omitempty"`
}
type Bind func(uint64) func(string, string, syscall.RawConn) error

// Encode produces ASCII-only JSON for JNI NewStringUTF. Standard UTF-8's
// four-byte characters are not valid JNI modified UTF-8; JSON escapes preserve
// their values without passing non-ASCII bytes through that boundary.
func Encode(response Response) []byte {
	data, err := json.Marshal(response)
	if err != nil || !utf8.Valid(data) {
		return []byte(`{"status":0}`)
	}
	out := make([]byte, 0, len(data))
	appendEscape := func(value rune) {
		const digits = "0123456789abcdef"
		out = append(out, '\\', 'u', digits[(value>>12)&15], digits[(value>>8)&15], digits[(value>>4)&15], digits[value&15])
	}
	for _, value := range string(data) {
		if value < utf8.RuneSelf {
			out = append(out, byte(value))
		} else if value <= 0xffff {
			appendEscape(value)
		} else {
			high, low := utf16.EncodeRune(value)
			appendEscape(high)
			appendEscape(low)
		}
	}
	return out
}

func Do(data []byte, bind Bind) Response {
	var r Request
	if len(data) > 32768 || json.Unmarshal(data, &r) != nil {
		return Response{}
	}
	result, e := exchange(r, bind)
	if e != nil {
		return Response{}
	}
	return result
}
func exchange(r Request, bind Bind) (Response, error) {
	invalid := errors.New("invalid control request")
	u, e := url.Parse(r.Origin)
	pin, pe := hex.DecodeString(r.Pin)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || pe != nil || len(pin) != 32 || r.Network == 0 || len(r.Addresses) == 0 || len(r.Addresses) > 16 || len(r.Bearer) != 43 || strings.ContainsAny(r.Bearer, "\r\n") {
		return Response{}, invalid
	}
	if r.Path != "/v1/network" && r.Path != "/v1/register" {
		return Response{}, invalid
	}
	if (r.Path == "/v1/register") != (len(r.Body) > 0) {
		return Response{}, invalid
	}
	for _, a := range r.Addresses {
		if _, e := netip.ParseAddr(a); e != nil {
			return Response{}, invalid
		}
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()
	dialer := &net.Dialer{Timeout: 6 * time.Second, Control: bind(r.Network)}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, MaxResponseHeaderBytes: 16384,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			var last error
			for _, a := range r.Addresses {
				var c net.Conn
				c, last = dialer.DialContext(ctx, network, net.JoinHostPort(a, port))
				if last == nil {
					return c, nil
				}
			}
			return nil, last
		},
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, MaxVersion: tls.VersionTLS13, InsecureSkipVerify: true, // Exact enrollment pin and validity replace CA/hostname trust.
			VerifyConnection: func(cs tls.ConnectionState) error {
				if len(cs.PeerCertificates) == 0 {
					return invalid
				}
				c := cs.PeerCertificates[0]
				sum := sha256.Sum256(c.Raw)
				if subtle.ConstantTimeCompare(sum[:], pin) != 1 || time.Now().Before(c.NotBefore) || time.Now().After(c.NotAfter) {
					return invalid
				}
				return nil
			},
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	method := "GET"
	if len(r.Body) > 0 {
		method = "POST"
	}
	req, e := http.NewRequestWithContext(ctx, method, r.Origin+r.Path, bytes.NewReader(r.Body))
	if e != nil {
		return Response{}, e
	}
	req.Header.Set("Authorization", "Bearer "+r.Bearer)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, e := client.Do(req)
	if e != nil {
		return Response{}, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Response{Status: resp.StatusCode}, nil
	}
	body, e := io.ReadAll(io.LimitReader(resp.Body, 131073))
	if e != nil || len(body) > 131072 || !utf8.Valid(body) || !json.Valid(body) {
		return Response{}, invalid
	}
	return Response{Status: 200, Body: body}, nil
}
