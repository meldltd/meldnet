package api

import (
	"meldnet/internal/config"
	"meldnet/internal/service"
	"meldnet/internal/vpn"
	"net/http/httptest"
	"strings"
	"testing"
)

func newService(t *testing.T) *service.Service {
	t.Helper()
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s, err := service.New(store, &vpn.Simulator{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestRejectHostOriginAndUnknownFields(t *testing.T) {
	h := Handler(newService(t))
	tests := []struct {
		name, host, origin, body string
		code                     int
	}{
		{"wrong host", "localhost", "", "{}", 403},
		{"browser origin", "meldnet", "https://example.com", "{}", 403},
		{"unknown field", "meldnet", "", `{"private_key":"secret"}`, 400},
		{"trailing json", "meldnet", "", `{} {}`, 400},
		{"oversized", "meldnet", "", `{"settings":{"name":"` + strings.Repeat("a", 129<<10) + `"}}`, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("PUT", "http://"+tt.host+"/v1/config", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", "application/json")
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tt.code {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}
