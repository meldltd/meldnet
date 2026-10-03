// Package control implements the primary registration service and autonomous client.
package control

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"meldnet/internal/config"
	"meldnet/internal/securefs"
)

type Options struct{ Listen, URL, Endpoint, Pool, Name string }
type Member struct {
	Name      string    `json:"name"`
	PublicKey string    `json:"public_key"`
	IP        string    `json:"ip"`
	LastSeen  time.Time `json:"last_seen"`
}
type Network struct {
	Pending bool     `json:"pending,omitempty"`
	Role    string   `json:"role"`
	Members []Member `json:"members"`
	Error   string   `json:"error,omitempty"`
}
type enrollment struct {
	URL    string `json:"url"`
	Pin    string `json:"pin"`
	Secret string `json:"secret"`
}
type JoinRequest struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}
type registerRequest struct {
	Name           string `json:"name"`
	PublicKey      string `json:"public_key"`
	CredentialHash string `json:"credential_hash"`
}
type registration struct {
	SettingsName string   `json:"name"`
	IP           string   `json:"ip"`
	Pool         string   `json:"pool"`
	Primary      Member   `json:"primary"`
	Endpoint     string   `json:"endpoint"`
	Members      []Member `json:"members"`
}
type invite struct {
	Hash           string
	Expires        time.Time
	PublicKey      string
	CredentialHash string
}
type device struct {
	Member
	Hash    string
	Revoked bool
}
type state struct {
	Version int
	Options *Options
	Devices []device
	Invites []invite
	Client  *clientState
}
type clientState struct {
	Enrollment enrollment
	Name       string
	Credential string
	Registered bool
}

func secret() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func hash(s string) string    { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func validHash(s string) bool { b, e := hex.DecodeString(s); return e == nil && len(b) == 32 }
func validSecret(s string) bool {
	b, e := base64.RawURLEncoding.DecodeString(s)
	return e == nil && len(b) == 32
}
func save(dir string, s state) error {
	b, e := json.Marshal(s)
	if e != nil {
		return e
	}
	return securefs.Write(filepath.Join(dir, "network.json"), b)
}
func load(dir string) (state, error) {
	var s state
	b, e := securefs.Read(filepath.Join(dir, "network.json"))
	if errors.Is(e, os.ErrNotExist) {
		s.Version = 1
		return s, nil
	}
	if e != nil {
		return s, e
	}
	if e = json.Unmarshal(b, &s); e != nil || s.Version != 1 {
		return s, errors.New("invalid network state")
	}
	return s, nil
}
func parseKey(key string) (enrollment, error) {
	var e enrollment
	if len(key) > 8192 || len(key) < 10 || key[:9] != "meldnet1." {
		return e, errors.New("invalid enrollment key")
	}
	b, err := base64.RawURLEncoding.DecodeString(key[9:])
	if err != nil {
		return e, errors.New("invalid enrollment key")
	}
	if json.Unmarshal(b, &e) != nil || !validHash(e.Pin) || !validSecret(e.Secret) || validURL(e.URL) != nil {
		return enrollment{}, errors.New("invalid enrollment key")
	}
	return e, nil
}
func validURL(s string) error {
	u, e := url.Parse(s)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return errors.New("public URL must be an HTTPS origin without a path")
	}
	return nil
}
func (o Options) validate() error {
	if err := (config.Settings{Name: o.Name, Addresses: []string{"10.77.0.1/32"}}).Validate(); err != nil {
		return err
	}
	if e := validURL(o.URL); e != nil {
		return e
	}
	host, port, e := net.SplitHostPort(o.Endpoint)
	if e != nil || host == "" {
		return errors.New("endpoint must be reachable host:UDP-port")
	}
	n, e := strconv.Atoi(port)
	if e != nil || n < 1 || n > 65535 {
		return errors.New("invalid endpoint port")
	}
	p, e := netip.ParsePrefix(o.Pool)
	if e != nil || !p.Addr().Is4() || !p.Addr().IsPrivate() || p != p.Masked() || p.Bits() < 16 || p.Bits() > 28 {
		return errors.New("pool must be a private IPv4 /16 through /28 network")
	}
	return nil
}

func (s state) validate() error {
	invalid := errors.New("invalid persisted network state; restore a complete daemon backup")
	if s.Version != 1 || (s.Options != nil && s.Client != nil) || len(s.Devices) > 128 || len(s.Invites) > 128 {
		return invalid
	}
	if s.Client != nil {
		c := s.Client
		if validURL(c.Enrollment.URL) != nil || !validHash(c.Enrollment.Pin) || !validSecret(c.Credential) || (!c.Registered && !validSecret(c.Enrollment.Secret)) {
			return invalid
		}
		if (config.Settings{Name: c.Name, Addresses: []string{"10.77.0.1/32"}}).Validate() != nil {
			return invalid
		}
	}
	if s.Options != nil {
		if e := s.Options.validate(); e != nil {
			return e
		}
		pool, _ := netip.ParsePrefix(s.Options.Pool)
		seen := map[string]bool{}
		for _, d := range s.Devices {
			ip, e := netip.ParseAddr(d.IP)
			if e != nil || !pool.Contains(ip) || ip == pool.Addr() || ip == pool.Addr().Next() || !pool.Contains(ip.Next()) || !validHash(d.Hash) || config.ValidateKey(d.PublicKey) != nil {
				return invalid
			}
			if (config.Settings{Name: d.Name, Addresses: []string{d.IP + "/32"}}).Validate() != nil || d.Name == s.Options.Name {
				return invalid
			}
			for _, k := range []string{"ip:" + d.IP, "name:" + d.Name, "key:" + d.PublicKey, "hash:" + d.Hash} {
				if seen[k] {
					return invalid
				}
				seen[k] = true
			}
		}
		for _, i := range s.Invites {
			if !validHash(i.Hash) || i.Expires.IsZero() {
				return invalid
			}
			if i.PublicKey != "" && (config.ValidateKey(i.PublicKey) != nil || !validHash(i.CredentialHash)) {
				return invalid
			}
		}
	} else if len(s.Devices) != 0 || len(s.Invites) != 0 {
		return invalid
	}
	return nil
}
