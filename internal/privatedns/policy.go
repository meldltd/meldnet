package privatedns

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/google/uuid"
	"meldnet/internal/securefs"
)

// policyStore isolates registry I/O so recovery is tested without host DNS edits.
type policyRule struct {
	Names  []string `json:"names"`
	Server string   `json:"server"`
}
type policyStore interface {
	Check(map[string]policyRule) error
	Create(string, policyRule) error
	RemoveOwned(string, policyRule) error
	Refresh() error
}
type policyResolver struct {
	store   policyStore
	journal string
	rules   map[string]policyRule
}

func newPolicyResolver(dir string, store policyStore) (*policyResolver, error) {
	r := &policyResolver{store: store, journal: filepath.Join(dir, "dns-policy.json"), rules: map[string]policyRule{}}
	data, err := securefs.Read(r.journal)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(data, &r.rules) != nil || len(r.rules) > 1 {
		return nil, errors.New("invalid DNS policy journal")
	}
	for id, rule := range r.rules {
		if _, err := uuid.Parse(id); err != nil || rule.Server != "127.0.0.1" || len(rule.Names) == 0 || len(rule.Names) > 8192 {
			return nil, errors.New("invalid DNS policy journal")
		}
	}
	if err := r.Restore(context.Background()); err != nil {
		return nil, err
	}
	return r, nil
}
func (r *policyResolver) Mode() string { return "Windows split DNS (NRPT)" }
func (r *policyResolver) Apply(ctx context.Context, _ string, server string, z *authority) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if server != "127.0.0.1" {
		return errors.New("Windows DNS requires the local aggregate")
	}
	if err := r.store.Check(r.rules); err != nil {
		return err
	}
	names := []string{Domain, "." + Domain}
	for _, domain := range z.current.Load().reverseDomains() {
		names = append(names, "."+domain)
	}
	sort.Strings(names)
	wanted := policyRule{Names: names, Server: server}
	for _, rule := range r.rules {
		if reflect.DeepEqual(rule, wanted) {
			return r.store.Refresh()
		}
	}
	if err := r.Restore(ctx); err != nil {
		return err
	}
	id := uuid.NewString()
	r.rules[id] = wanted
	data, err := json.Marshal(r.rules)
	if err != nil {
		return err
	}
	if err := securefs.Write(r.journal, data); err != nil {
		delete(r.rules, id)
		return err
	}
	if err := r.store.Create(id, wanted); err != nil {
		return err
	}
	return r.store.Refresh()
}
func (r *policyResolver) Restore(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for id, rule := range r.rules {
		if err := r.store.RemoveOwned(id, rule); err != nil {
			return err
		}
	}
	if len(r.rules) > 0 {
		if err := r.store.Refresh(); err != nil {
			return err
		}
	}
	if err := os.Remove(r.journal); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	r.rules = map[string]policyRule{}
	return nil
}
