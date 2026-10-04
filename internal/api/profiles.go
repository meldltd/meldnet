package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"meldnet/internal/profiles"
	"meldnet/internal/vpn"
)

// ProfilesHandler preserves the v1 default-profile API and adds explicitly scoped
// endpoints. Its authentication still comes from the same peer-credential socket.
func ProfilesHandler(manager *profiles.Manager) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Host != "meldnet" || r.Header.Get("Origin") != "" {
			respond(w, 403, errorResponse{"only local Meldnet clients are accepted"})
			return
		}
		if r.URL.Path == "/v1/networks" && r.Method == "GET" {
			respond(w, 200, manager.List(r.Context()))
			return
		}
		if r.URL.Path == "/v1/networks/join" && r.Method == "POST" {
			var req profiles.JoinRequest
			if !decodeProfile(w, r, &req) {
				return
			}
			if err := manager.Join(r.Context(), req); err != nil {
				profileError(w, err)
				return
			}
			respond(w, 200, map[string]bool{"ok": true})
			return
		}
		id := profiles.Default
		path := r.URL.Path
		if strings.HasPrefix(path, "/v1/networks/") {
			parts := strings.SplitN(strings.TrimPrefix(path, "/v1/networks/"), "/", 2)
			if len(parts) != 2 {
				http.NotFound(w, r)
				return
			}
			id = parts[0]
			path = "/v1/" + parts[1]
		}
		runtime, err := manager.Runtime(id)
		if err != nil {
			profileError(w, err)
			return
		}
		switch {
		case path == "/v1/network" && r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/networks/"):
			p, e := manager.Profile(r.Context(), id)
			if e != nil {
				profileError(w, e)
				return
			}
			respond(w, 200, p.Network)
			return
		case path == "/v1/up" && r.Method == "POST":
			err = manager.Connect(r.Context(), id)
		case path == "/v1/down" && r.Method == "POST":
			err = manager.Disconnect(r.Context(), id)
		case path == "/v1/autoconnect" && r.Method == "PUT":
			var req struct {
				AutoConnect *bool `json:"auto_connect"`
			}
			if !decodeProfile(w, r, &req) {
				return
			}
			if req.AutoConnect == nil {
				respond(w, 400, errorResponse{"auto_connect is required"})
				return
			}
			err = manager.SetAuto(id, *req.AutoConnect)
		case path == "/v1/network/join" && r.Method == "POST":
			var req struct {
				Name string `json:"name"`
				Key  string `json:"key"`
			}
			if !decodeProfile(w, r, &req) {
				return
			}
			err = manager.Join(r.Context(), profiles.JoinRequest{ID: id, Name: req.Name, Key: req.Key, Connect: true, AutoConnect: true})
		default:
			clone := r.Clone(r.Context())
			url := *r.URL
			url.Path = path
			clone.URL = &url
			Handler(runtime.Service, runtime.Manager).ServeHTTP(w, clone)
			return
		}
		if err != nil {
			profileError(w, err)
			return
		}
		respond(w, 200, map[string]bool{"ok": true})
	})
}
func decodeProfile(w http.ResponseWriter, r *http.Request, out any) bool {
	if r.Header.Get("Content-Type") != "application/json" {
		respond(w, 415, errorResponse{"Content-Type must be application/json"})
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	dec.DisallowUnknownFields()
	if dec.Decode(out) != nil || dec.Decode(new(any)) != io.EOF {
		respond(w, 400, errorResponse{"invalid network profile request"})
		return false
	}
	return true
}
func profileError(w http.ResponseWriter, err error) {
	if errors.Is(err, profiles.ErrNotFound) {
		respond(w, 404, errorResponse{err.Error()})
		return
	}
	if errors.Is(err, vpn.ErrOverlap) {
		respond(w, 409, errorResponse{err.Error()})
		return
	}
	report(w, err)
}
