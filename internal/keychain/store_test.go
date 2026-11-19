package keychain

import (
	"errors"
	"testing"
)

// memStore is an in-memory secretStore; these tests never touch a keychain.
type memStore map[string]string

func (m memStore) get(a string) (string, error) {
	s, ok := m[a]
	if !ok {
		return "", ErrNotFound
	}
	return s, nil
}
func (m memStore) set(a, s string) error { m[a] = s; return nil }
func (m memStore) del(a string) error {
	if _, ok := m[a]; !ok {
		return ErrNotFound
	}
	delete(m, a)
	return nil
}

func TestMigratingStore_getMigratesLegacyItem(t *testing.T) {
	bound, legacy := memStore{}, memStore{"prod": "secret"}
	m := migratingStore{bound: bound, legacy: legacy}

	got, err := m.get("prod")
	if err != nil || got != "secret" {
		t.Fatalf("get = %q, %v; want secret, nil", got, err)
	}
	if bound["prod"] != "secret" {
		t.Errorf("bound store not populated: %v", bound)
	}
	if _, ok := legacy["prod"]; ok {
		t.Errorf("legacy item not deleted: %v", legacy)
	}
}

func TestMigratingStore_boundWinsOverLegacy(t *testing.T) {
	bound, legacy := memStore{"prod": "new"}, memStore{"prod": "old"}
	got, err := migratingStore{bound: bound, legacy: legacy}.get("prod")
	if err != nil || got != "new" {
		t.Fatalf("get = %q, %v; want new, nil", got, err)
	}
	if legacy["prod"] != "old" {
		t.Errorf("a bound hit must not consult the legacy store")
	}
}

func TestMigratingStore_missInBoth(t *testing.T) {
	_, err := migratingStore{bound: memStore{}, legacy: memStore{}}.get("prod")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestMigratingStore_setDropsLegacyCopy(t *testing.T) {
	bound, legacy := memStore{}, memStore{"prod": "stale"}
	if err := (migratingStore{bound: bound, legacy: legacy}).set("prod", "fresh"); err != nil {
		t.Fatal(err)
	}
	if bound["prod"] != "fresh" || len(legacy) != 0 {
		t.Errorf("bound=%v legacy=%v", bound, legacy)
	}
}

func TestMigratingStore_del(t *testing.T) {
	tests := []struct {
		name          string
		bound, legacy memStore
		wantNotFound  bool
	}{
		{"bound only", memStore{"p": "x"}, memStore{}, false},
		{"legacy only", memStore{}, memStore{"p": "x"}, false},
		{"both", memStore{"p": "x"}, memStore{"p": "y"}, false},
		{"neither", memStore{}, memStore{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := migratingStore{bound: tt.bound, legacy: tt.legacy}.del("p")
			if errors.Is(err, ErrNotFound) != tt.wantNotFound {
				t.Fatalf("del err = %v, wantNotFound %v", err, tt.wantNotFound)
			}
			if len(tt.bound)+len(tt.legacy) != 0 {
				t.Errorf("items left: bound=%v legacy=%v", tt.bound, tt.legacy)
			}
		})
	}
}

func TestSelectStore_testBinaryUsesLegacyCLI(t *testing.T) {
	// go test binaries are never Developer-ID signed, and selectStore also
	// refuses explicitly under testing, so no bound item can be created here.
	if _, ok := selectStore("x.bound", "x").(cliStore); !ok {
		t.Fatal("test binary must use the security CLI store")
	}
}

func TestDecodeSecurityOutput(t *testing.T) {
	tests := []struct{ in, want string }{
		{`{"d":"Muller"}`, `{"d":"Muller"}`},
		{"7b2264223a224dc3bc6c6c6572227d", `{"d":"Müller"}`}, // security -w hex for non-ASCII
		{"deadbeef", "deadbeef"},                             // ASCII that looks like hex
		{"6162", "6162"},                                     // decodes to ASCII: keep
		{"", ""},
	}
	for _, tt := range tests {
		if got := decodeSecurityOutput(tt.in); got != tt.want {
			t.Errorf("decodeSecurityOutput(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
