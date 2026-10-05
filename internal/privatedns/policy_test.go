package privatedns

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type fakePolicies struct {
	rules                                  map[string]policyRule
	createError, removeError, refreshError error
	beforeCreate                           func()
}

func (s *fakePolicies) Check(map[string]policyRule) error { return nil }
func (s *fakePolicies) Create(id string, rule policyRule) error {
	if s.beforeCreate != nil {
		s.beforeCreate()
	}
	s.rules[id] = rule
	return s.createError
}
func (s *fakePolicies) RemoveOwned(id string, rule policyRule) error {
	if s.removeError != nil {
		return s.removeError
	}
	if actual, exists := s.rules[id]; exists && !reflect.DeepEqual(actual, rule) {
		return errors.New("external edit")
	}
	delete(s.rules, id)
	return nil
}
func (s *fakePolicies) Refresh() error { return s.refreshError }

func TestPolicyJournalBeforeWriteAndCrashRecovery(t *testing.T) {
	dir := t.TempDir()
	store := &fakePolicies{rules: map[string]policyRule{}, createError: errors.New("partial write")}
	r, err := newPolicyResolver(dir, store)
	if err != nil {
		t.Fatal(err)
	}
	store.beforeCreate = func() {
		if _, err := os.Stat(r.journal); err != nil {
			t.Fatal("intent must be journaled first", err)
		}
	}
	z, err := makeZone(Settings{Server: "127.0.0.1", Primary: true, Networks: map[string]Settings{"work": testSettings()}})
	if err != nil {
		t.Fatal(err)
	}
	a := &authority{}
	a.current.Store(z)
	if err := r.Apply(context.Background(), "", "127.0.0.1", a); err == nil {
		t.Fatal("partial write must fail")
	}
	if len(store.rules) != 1 {
		t.Fatal("missing simulated registry write")
	}
	if _, err := newPolicyResolver(dir, store); err != nil {
		t.Fatal(err)
	}
	if len(store.rules) != 0 {
		t.Fatal("crashed policy not recovered")
	}
	if _, err := os.Stat(filepath.Join(dir, "dns-policy.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("journal not removed", err)
	}
}

func TestPolicyPreservesExternalEditAndFailedCleanup(t *testing.T) {
	dir := t.TempDir()
	store := &fakePolicies{rules: map[string]policyRule{}}
	r, err := newPolicyResolver(dir, store)
	if err != nil {
		t.Fatal(err)
	}
	z, err := makeZone(Settings{Networks: map[string]Settings{"work": testSettings()}})
	if err != nil {
		t.Fatal(err)
	}
	a := &authority{}
	a.current.Store(z)
	if err := r.Apply(context.Background(), "", "127.0.0.1", a); err != nil {
		t.Fatal(err)
	}
	var id string
	for key := range store.rules {
		id = key
	}
	original := store.rules[id]
	store.rules[id] = policyRule{Names: []string{".external"}, Server: "192.0.2.53"}
	if err := r.Restore(context.Background()); err == nil {
		t.Fatal("external edits must be preserved")
	}
	if _, err := os.Stat(r.journal); err != nil {
		t.Fatal("journal must remain", err)
	}
	store.rules[id] = original
	store.refreshError = errors.New("refresh failed")
	if err := r.Restore(context.Background()); err == nil {
		t.Fatal("refresh failure hidden")
	}
	if _, err := os.Stat(r.journal); err != nil {
		t.Fatal("refresh failure lost journal")
	}
	store.refreshError = nil
	if err := r.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
}
