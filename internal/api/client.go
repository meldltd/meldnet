package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"meldnet/internal/config"
	"meldnet/internal/control"
	"meldnet/internal/profiles"
	"meldnet/internal/service"
)

const DefaultSocket = "/var/run/meldnet/control.sock"

type Client struct {
	http    *http.Client
	profile string
}

func NewClient(socket string) *Client {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", socket)
	}, DisableCompression: true}
	return &Client{http: &http.Client{Transport: transport, Timeout: 40 * time.Second}}
}
func (c *Client) Close() { c.http.CloseIdleConnections() }

func (c *Client) request(ctx context.Context, method, path string, input, output any) error {
	if c.profile != "" && !strings.HasPrefix(path, "/v1/networks") {
		path = "/v1/networks/" + url.PathEscape(c.profile) + strings.TrimPrefix(path, "/v1")
	}
	var body io.Reader
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://meldnet"+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return errors.New("cannot reach meldnetd; check that it is running, the socket path matches, and your UID has access")
	}
	defer res.Body.Close()
	reader := io.LimitReader(res.Body, 1<<20)
	if res.StatusCode != http.StatusOK {
		var failure errorResponse
		if err := json.NewDecoder(reader).Decode(&failure); err != nil {
			return fmt.Errorf("daemon returned HTTP %d", res.StatusCode)
		}
		return errors.New(failure.Error)
	}
	if output != nil {
		return json.NewDecoder(reader).Decode(output)
	}
	_, err = io.Copy(io.Discard, reader)
	return err
}
func (c *Client) Status(ctx context.Context) (service.Status, error) {
	var s service.Status
	err := c.request(ctx, "GET", "/v1/status", nil, &s)
	return s, err
}
func (c *Client) Configure(ctx context.Context, s config.Settings, revision uint64) (config.Public, error) {
	var p config.Public
	err := c.request(ctx, "PUT", "/v1/config", ConfigRequest{Revision: revision, Settings: s}, &p)
	return p, err
}
func (c *Client) Connect(ctx context.Context) error {
	return c.request(ctx, "POST", "/v1/up", nil, nil)
}
func (c *Client) Disconnect(ctx context.Context) error {
	return c.request(ctx, "POST", "/v1/down", nil, nil)
}

func (c *Client) Join(ctx context.Context, name, key string) error {
	return c.request(ctx, "POST", "/v1/network/join", control.JoinRequest{Name: name, Key: key}, nil)
}
func (c *Client) Invite(ctx context.Context) (string, error) {
	var r struct {
		Key string `json:"key"`
	}
	e := c.request(ctx, "POST", "/v1/network/invite", nil, &r)
	return r.Key, e
}
func (c *Client) Network(ctx context.Context) (control.Network, error) {
	var n control.Network
	e := c.request(ctx, "GET", "/v1/network", nil, &n)
	return n, e
}
func (c *Client) Revoke(ctx context.Context, name string) error {
	return c.request(ctx, "DELETE", "/v1/network/members/"+url.PathEscape(name), nil, nil)
}

// ForNetwork returns an independent API view; polling one TUI cannot change
// which profile another in-flight request targets.
func (c *Client) ForNetwork(id string) *Client { return &Client{http: c.http, profile: id} }
func (c *Client) SelectedNetwork() string {
	if c.profile == "" {
		return "default"
	}
	return c.profile
}
func (c *Client) Networks(ctx context.Context) ([]profiles.Profile, error) {
	var out []profiles.Profile
	err := c.request(ctx, "GET", "/v1/networks", nil, &out)
	return out, err
}
func (c *Client) JoinNetwork(ctx context.Context, req profiles.JoinRequest) error {
	return c.request(ctx, "POST", "/v1/networks/join", req, nil)
}
func (c *Client) SetAutoConnect(ctx context.Context, enabled bool) error {
	return c.request(ctx, "PUT", "/v1/autoconnect", map[string]bool{"auto_connect": enabled}, nil)
}
