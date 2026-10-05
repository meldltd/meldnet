package privatedns

import (
	"errors"
	"reflect"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const localPolicy = `SYSTEM\CurrentControlSet\Services\Dnscache\Parameters\DnsPolicyConfig`
const groupPolicy = `SOFTWARE\Policies\Microsoft\Windows NT\DNSClient\DnsPolicyConfig`
const policyMarker = "Meldnet private DNS"

type registryPolicy struct{}

func newResolver(dir string) (resolver, error) { return newPolicyResolver(dir, registryPolicy{}) }
func (registryPolicy) Check(owned map[string]policyRule) error {
	// Group policies override local NRPT rules. Refuse rather than reporting DNS
	// active while private queries silently go to an organization's public DNS.
	for _, path := range []string{groupPolicy, localPolicy} {
		key, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.READ)
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
			continue
		}
		if err != nil {
			return err
		}
		names, err := key.ReadSubKeyNames(-1)
		key.Close()
		if err != nil {
			return err
		}
		for _, name := range names {
			_, ours := owned[strings.Trim(name, "{}")]
			if path == groupPolicy || !ours {
				return errors.New("existing Windows DNS policy found; private DNS cannot safely override it")
			}
		}
	}
	return nil
}
func (registryPolicy) Create(id string, rule policyRule) error {
	k, existed, err := registry.CreateKey(registry.LOCAL_MACHINE, localPolicy+`\{`+id+`}`, registry.ALL_ACCESS)
	if err != nil {
		return err
	}
	defer k.Close()
	if existed {
		return errors.New("DNS policy key already exists")
	}
	// The private journal has already been durably written.
	if err := k.SetStringValue("Comment", policyMarker); err != nil {
		return err
	}
	if err := k.SetDWordValue("Version", 2); err != nil {
		return err
	}
	if err := k.SetStringsValue("Name", rule.Names); err != nil {
		return err
	}
	if err := k.SetStringValue("GenericDNSServers", rule.Server); err != nil {
		return err
	}
	return k.SetDWordValue("ConfigOptions", 8)
}
func (registryPolicy) RemoveOwned(id string, rule policyRule) error {
	path := localPolicy + `\{` + id + `}`
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.READ)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) {
		return nil
	}
	if err != nil {
		return err
	}
	defer k.Close()
	values, err := k.ReadValueNames(-1)
	if err != nil {
		return err
	}
	// Every present value must match. This also recovers a crash partway through
	// creation; unknown values or administrator replacements retain the journal.
	for _, name := range values {
		match := false
		switch name {
		case "Comment":
			v, _, e := k.GetStringValue(name)
			match = e == nil && v == policyMarker
		case "Version":
			v, _, e := k.GetIntegerValue(name)
			match = e == nil && v == 2
		case "ConfigOptions":
			v, _, e := k.GetIntegerValue(name)
			match = e == nil && v == 8
		case "GenericDNSServers":
			v, _, e := k.GetStringValue(name)
			match = e == nil && v == rule.Server
		case "Name":
			v, _, e := k.GetStringsValue(name)
			match = e == nil && reflect.DeepEqual(v, rule.Names)
		}
		if !match {
			return errors.New("Windows DNS policy externally modified; recovery journal retained")
		}
	}
	return registry.DeleteKey(registry.LOCAL_MACHINE, path)
}
func (registryPolicy) Refresh() error {
	// Dnscache observes policy registry notifications. Invalidate cached answers
	// after publication/removal using the built-in DNS API, never a subprocess.
	proc := windows.NewLazySystemDLL("dnsapi.dll").NewProc("DnsFlushResolverCache")
	if err := proc.Find(); err != nil {
		return err
	}
	ok, _, err := proc.Call()
	if ok == 0 {
		return err
	}
	return nil
}
