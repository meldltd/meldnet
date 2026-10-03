// Package api is the versioned local control boundary, shared by all frontends.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"meldnet/internal/config"
	"meldnet/internal/control"
	"meldnet/internal/service"
)

type ConfigRequest struct {
	Revision uint64          `json:"revision"`
	Settings config.Settings `json:"settings"`
}
type errorResponse struct {
	Error string `json:"error"`
}

func Handler(s *service.Service, managers ...*control.Manager) http.Handler {
	mux := http.NewServeMux()
	if len(managers) > 0 {
		m := managers[0]
		mux.HandleFunc("GET /v1/network", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, m.Snapshot()) })
		mux.HandleFunc("POST /v1/network/invite", func(w http.ResponseWriter, r *http.Request) {
			key, e := m.Invite()
			if e != nil {
				report(w, e)
				return
			}
			respond(w, 200, map[string]string{"key": key})
		})
		mux.HandleFunc("DELETE /v1/network/members/{name}", func(w http.ResponseWriter, r *http.Request) {
			if e := m.Revoke(r.PathValue("name")); e != nil {
				report(w, e)
				return
			}
			respond(w, 200, map[string]bool{"ok": true})
		})
		mux.HandleFunc("POST /v1/network/join", func(w http.ResponseWriter, r *http.Request) {
			var req control.JoinRequest
			dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
			dec.DisallowUnknownFields()
			if dec.Decode(&req) != nil || dec.Decode(new(any)) != io.EOF {
				respond(w, 400, errorResponse{"invalid join request"})
				return
			}
			if e := m.Join(r.Context(), req); e != nil {
				report(w, e)
				return
			}
			respond(w, 200, map[string]bool{"ok": true})
		})
	}
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) { respond(w, http.StatusOK, s.Status(r.Context())) })
	mux.HandleFunc("PUT /v1/config", func(w http.ResponseWriter, r *http.Request) {
		var req ConfigRequest
		if r.Header.Get("Content-Type") != "application/json" {
			respond(w, 415, errorResponse{"Content-Type must be application/json"})
			return
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			respond(w, 400, errorResponse{"invalid configuration JSON or request too large"})
			return
		}
		if err := dec.Decode(new(any)); err != io.EOF {
			respond(w, 400, errorResponse{"request must contain exactly one JSON object"})
			return
		}
		p, err := s.Configure(r.Context(), req.Settings, req.Revision)
		if err != nil {
			report(w, err)
			return
		}
		respond(w, 200, p)
	})
	mux.HandleFunc("POST /v1/up", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Connect(r.Context()); err != nil {
			report(w, err)
			return
		}
		respond(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /v1/down", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Disconnect(r.Context()); err != nil {
			report(w, err)
			return
		}
		respond(w, 200, map[string]bool{"ok": true})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Host != "meldnet" || r.Header.Get("Origin") != "" {
			respond(w, 403, errorResponse{"only local Meldnet clients are accepted"})
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func report(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	if errors.Is(err, service.ErrConflict) || errors.Is(err, service.ErrRunning) {
		code = http.StatusConflict
	}
	respond(w, code, errorResponse{err.Error()})
}
func respond(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}
