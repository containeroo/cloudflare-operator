package controller

import (
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/cloudflare/cloudflare-go"
)

var (
	errNoAccountForZone = errors.New("no account manages the requested zone")
	errMultipleAccounts = errors.New("multiple accounts manage the requested zone")
)

// accountInfo holds the data we need to interact with Cloudflare for a single account.
type accountInfo struct {
	api          *cloudflare.API
	managedZones map[string]struct{}
	token        string
}

// AccountManager keeps track of available Cloudflare accounts and the zones they manage.
type AccountManager struct {
	mu             sync.RWMutex
	accounts       map[string]*accountInfo
	zoneToAccounts map[string]map[string]struct{}
}

// NewAccountManager returns an initialized AccountManager instance.
func NewAccountManager() *AccountManager {
	return &AccountManager{
		accounts:       make(map[string]*accountInfo),
		zoneToAccounts: make(map[string]map[string]struct{}),
	}
}

// UpsertAccount registers or updates an account and its managed zones.
func (m *AccountManager) UpsertAccount(name string, api *cloudflare.API, token string, managedZones []string) {
	canonicalZones := normalizeZones(managedZones)

	m.mu.Lock()
	defer m.mu.Unlock()

	// remove old zone membership for this account
	if existing, ok := m.accounts[name]; ok {
		for zone := range existing.managedZones {
			m.removeZoneMembership(zone, name)
		}
	}

	zoneSet := make(map[string]struct{}, len(canonicalZones))
	for _, zone := range canonicalZones {
		zoneSet[zone] = struct{}{}
		if _, ok := m.zoneToAccounts[zone]; !ok {
			m.zoneToAccounts[zone] = make(map[string]struct{})
		}
		m.zoneToAccounts[zone][name] = struct{}{}
	}

	m.accounts[name] = &accountInfo{
		api:          api,
		managedZones: zoneSet,
		token:        token,
	}
}

// RemoveAccount unregisters an account and clears any zone mappings.
func (m *AccountManager) RemoveAccount(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entry, ok := m.accounts[name]
	if !ok {
		return
	}

	for zone := range entry.managedZones {
		m.removeZoneMembership(zone, name)
	}

	delete(m.accounts, name)
}

// GetAccount returns the stored account info by name.
func (m *AccountManager) GetAccount(name string) (*cloudflare.API, string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	entry, ok := m.accounts[name]
	if !ok {
		return nil, "", false
	}

	return entry.api, entry.token, true
}

// AccountForZone returns the Cloudflare client for the account managing the provided zone.
// If no account manages the zone, errNoAccountForZone is returned.
// If multiple accounts manage the same zone, errMultipleAccounts is returned along with the list of candidates.
func (m *AccountManager) AccountForZone(zone string) (*cloudflare.API, string, error) {
	canonical := canonicalZone(zone)

	m.mu.RLock()
	defer m.mu.RUnlock()

	accounts, ok := m.zoneToAccounts[canonical]
	if !ok || len(accounts) == 0 {
		return nil, "", errNoAccountForZone
	}

	if len(accounts) > 1 {
		names := make([]string, 0, len(accounts))
		for name := range accounts {
			names = append(names, name)
		}
		sort.Strings(names)
		return nil, strings.Join(names, ", "), errMultipleAccounts
	}

	var accountName string
	for name := range accounts {
		accountName = name
	}

	entry, ok := m.accounts[accountName]
	if !ok {
		return nil, accountName, errNoAccountForZone
	}

	return entry.api, accountName, nil
}

func (m *AccountManager) removeZoneMembership(zone, accountName string) {
	canonical := canonicalZone(zone)
	members, ok := m.zoneToAccounts[canonical]
	if !ok {
		return
	}

	delete(members, accountName)
	if len(members) == 0 {
		delete(m.zoneToAccounts, canonical)
	}
}

func normalizeZones(zones []string) []string {
	result := make([]string, 0, len(zones))
	seen := make(map[string]struct{}, len(zones))
	for _, zone := range zones {
		canonical := canonicalZone(zone)
		if canonical == "" {
			continue
		}
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		result = append(result, canonical)
	}

	sort.Strings(result)
	return result
}

func canonicalZone(zone string) string {
	zone = strings.TrimSpace(zone)
	if zone == "" {
		return ""
	}
	return strings.ToLower(zone)
}
