package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestNextOutlet_FirstOutletSkipsPrimaryPort: the daemon's own proxy port is
// never handed to an outlet, so the primary listener cannot be taken over.
func TestNextOutlet_FirstOutletSkipsPrimaryPort(t *testing.T) {
	o := nextOutlet(nil, 1080)
	if o.Port != 1081 {
		t.Errorf("port = %d, want 1081 (1080 is the primary)", o.Port)
	}
	if o.ID != 1 {
		t.Errorf("id = %d, want 1", o.ID)
	}
}

// TestNextOutlet_NoPrimaryPort: with no primary configured the first free port
// is 1080 itself.
func TestNextOutlet_NoPrimaryPort(t *testing.T) {
	if o := nextOutlet(nil, 0); o.Port != 1080 {
		t.Errorf("port = %d, want 1080", o.Port)
	}
}

// TestNextOutlet_PortReuse: after the lowest outlet is removed its port is
// free again and the next outlet takes it, instead of leaving a permanent
// gap in the sequence.
func TestNextOutlet_PortReuse(t *testing.T) {
	// Outlet 1 (1081) was removed; outlet 2 (1082) remains.
	outlets := []daemonOutlet{{ID: 2, Port: 1082}}
	o := nextOutlet(outlets, 1080)
	if o.Port != 1081 {
		t.Errorf("port = %d, want 1081 (freed by the removed outlet)", o.Port)
	}
	if o.ID != 3 {
		t.Errorf("id = %d, want 3", o.ID)
	}
}

// TestNextOutlet_SkipsHoles: an already-taken low port is not handed out
// twice just because a higher one is free.
func TestNextOutlet_SkipsHoles(t *testing.T) {
	outlets := []daemonOutlet{{ID: 1, Port: 1081}, {ID: 2, Port: 1083}}
	o := nextOutlet(outlets, 1080)
	if o.Port != 1082 {
		t.Errorf("port = %d, want 1082", o.Port)
	}
}

// TestNextOutlet_IDNotReused covers the removal case: after `rm` of a
// mid-sequence outlet, len(outlets)+1 would collide with an existing id, and
// the daemon keys live rotators by id, so two outlets would collapse into one.
func TestNextOutlet_IDNotReused(t *testing.T) {
	// Outlets 1 and 3 exist; outlet 2 was removed.
	outlets := []daemonOutlet{{ID: 1, Port: 1081}, {ID: 3, Port: 1082}}
	o := nextOutlet(outlets, 1080)
	if o.ID != 4 {
		t.Errorf("id = %d, want 4 (max existing id + 1)", o.ID)
	}
	if o.ID == 2 || o.ID == 1 || o.ID == 3 {
		t.Errorf("id %d collides with an existing outlet", o.ID)
	}
}

// TestNextOutlet_IgnoresPortOrdering: ids and ports are allocated
// independently, so a gapped port list must not distort either.
func TestNextOutlet_IgnoresPortOrdering(t *testing.T) {
	outlets := []daemonOutlet{{ID: 5, Port: 1090}, {ID: 2, Port: 1085}}
	o := nextOutlet(outlets, 1080)
	if o.Port != 1081 {
		t.Errorf("port = %d, want 1081", o.Port)
	}
	if o.ID != 6 {
		t.Errorf("id = %d, want 6", o.ID)
	}
}

// TestSplitCountries covers the --country parsing of `daemon proxy add`:
// one outlet per comma-separated country, trimmed, case-insensitively
// deduplicated, with an empty value meaning "no country filter".
func TestSplitCountries(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"VN", []string{"VN"}},
		{"VN,SG,ID", []string{"VN", "SG", "ID"}},
		{" VN , sg ,VN,, ID ", []string{"VN", "sg", "ID"}},
		{"", []string{"all"}},
		{" , ", []string{"all"}},
		{"all", []string{"all"}},
		{"vn,VN", []string{"vn"}},
	}
	for _, tc := range cases {
		got := splitCountries(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("splitCountries(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("splitCountries(%q) = %v, want %v", tc.in, got, tc.want)
				break
			}
		}
	}
}

// TestAppendOutletsOnePerCountry: --country VN,SG,ID produces three outlets
// with distinct ports and ids, each filtered to its own country, while the
// primary port stays reserved.
func TestAppendOutletsOnePerCountry(t *testing.T) {
	outlets, created := appendOutlets(nil, splitCountries("VN,SG,ID"), "ss", 1080)

	if len(created) != 3 || len(outlets) != 3 {
		t.Fatalf("created %d / appended %d outlets, want 3/3", len(created), len(outlets))
	}
	wantCountries := []string{"VN", "SG", "ID"}
	seenPorts := map[int]bool{1080: true}
	seenIDs := map[int]bool{}
	for i, o := range created {
		if o.Country != wantCountries[i] {
			t.Errorf("outlet %d country = %q, want %q", i, o.Country, wantCountries[i])
		}
		if o.Protocol != "ss" {
			t.Errorf("outlet %d protocol = %q, want ss", i, o.Protocol)
		}
		if o.Addr != fmt.Sprintf("127.0.0.1:%d", o.Port) {
			t.Errorf("outlet %d addr = %q, want 127.0.0.1:%d", i, o.Addr, o.Port)
		}
		if seenPorts[o.Port] {
			t.Errorf("outlet %d reuses port %d", i, o.Port)
		}
		if seenIDs[o.ID] {
			t.Errorf("outlet %d reuses id %d", i, o.ID)
		}
		seenPorts[o.Port] = true
		seenIDs[o.ID] = true
	}
}

// TestAppendOutletsContinuesSequence: a later batch keeps allocating from the
// ports and ids the previous outlets hold.
func TestAppendOutletsContinuesSequence(t *testing.T) {
	outlets, created := appendOutlets(nil, splitCountries("VN"), "", 1080)
	if len(created) != 1 || created[0].Port != 1081 || created[0].ID != 1 {
		t.Fatalf("first batch = %+v, want port 1081 id 1", created)
	}

	outlets, created = appendOutlets(outlets, splitCountries("SG"), "", 1080)
	if len(outlets) != 2 || len(created) != 1 {
		t.Fatalf("second batch appended %d outlets (total %d), want 1/2", len(created), len(outlets))
	}
	if created[0].Port != 1082 || created[0].ID != 2 {
		t.Errorf("second batch = %+v, want port 1082 id 2", created[0])
	}

	outlets, created = appendOutlets(outlets, splitCountries(""), "", 1080)
	if len(created) != 1 || created[0].Country != "all" || created[0].Port != 1083 {
		t.Errorf("empty country batch = %+v, want single 'all' outlet on 1083", created)
	}
}

// TestWriteOutletIntentAtomic: the daemon reads the intent file on every
// reconcile, so a reader must never observe a partial list. A truncating
// write made the daemon see outlets missing from its own intent and tear
// their rotators down, then start them again a second later.
func TestWriteOutletIntentAtomic(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".rotaze"), 0700); err != nil {
		t.Fatal(err)
	}

	want := []daemonOutlet{
		{ID: 1, Port: 1081, Country: "VN"},
		{ID: 2, Port: 1082, Country: "SG"},
	}
	if err := writeOutletIntent(want); err != nil {
		t.Fatalf("writeOutletIntent: %v", err)
	}
	got := readOutletIntent()
	if len(got) != 2 || got[0].Port != 1081 || got[1].Country != "SG" {
		t.Fatalf("round trip = %+v, want 2 outlets %+v", got, want)
	}

	// A reader racing a writer sees the full list every time, never a short
	// (or empty) one.
	bad := make(chan []daemonOutlet, 1)
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if got := readOutletIntent(); len(got) != 2 {
				select {
				case bad <- got:
				default:
				}
				return
			}
		}
	}()
	for i := 0; i < 200; i++ {
		if err := writeOutletIntent(want); err != nil {
			t.Fatalf("writeOutletIntent: %v", err)
		}
	}
	close(stop)
	select {
	case got := <-bad:
		t.Errorf("concurrent reader observed a partial intent list: %+v", got)
	default:
	}

	// The temp file must not survive the rename.
	entries, err := os.ReadDir(filepath.Join(home, ".rotaze"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "outlets.json" {
		t.Errorf("intent dir holds %v, want only outlets.json", entries)
	}
}

// TestParseOutletIDs covers the argument forms `daemon proxy rm` accepts:
// separate IDs, comma-separated IDs, and "all", mixed in any order.
func TestParseOutletIDs(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantIDs map[int]bool
		wantAll bool
		wantErr bool
	}{
		{name: "single id", args: []string{"1"}, wantIDs: map[int]bool{1: true}},
		{name: "several ids", args: []string{"1", "2"}, wantIDs: map[int]bool{1: true, 2: true}},
		{name: "comma separated", args: []string{"1,2"}, wantIDs: map[int]bool{1: true, 2: true}},
		{name: "spaces and empty parts", args: []string{" 1 , ,2 "}, wantIDs: map[int]bool{1: true, 2: true}},
		{name: "duplicates collapse", args: []string{"1", "1"}, wantIDs: map[int]bool{1: true}},
		{name: "all", args: []string{"all"}, wantIDs: map[int]bool{}, wantAll: true},
		{name: "all with an id", args: []string{"all,2"}, wantIDs: map[int]bool{2: true}, wantAll: true},
		{name: "case insensitive all", args: []string{"2", "ALL"}, wantIDs: map[int]bool{2: true}, wantAll: true},
		{name: "not a number", args: []string{"x"}, wantErr: true},
		{name: "mixed valid and invalid", args: []string{"1", "x"}, wantErr: true},
		{name: "zero", args: []string{"0"}, wantErr: true},
		{name: "negative", args: []string{"-1"}, wantErr: true},
		{name: "empty argument", args: []string{""}, wantErr: true},
		{name: "no argument", args: nil, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ids, all, err := parseOutletIDs(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseOutletIDs(%q) = %v, %v, want error", tt.args, ids, all)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseOutletIDs(%q): %v", tt.args, err)
			}
			if all != tt.wantAll {
				t.Errorf("all = %v, want %v", all, tt.wantAll)
			}
			if !reflect.DeepEqual(ids, tt.wantIDs) {
				t.Errorf("ids = %v, want %v", ids, tt.wantIDs)
			}
		})
	}
}

// TestRemoveOutlets: rm is all-or-nothing. The helper removes whatever was
// named and reports unknown IDs in missing; the caller refuses the whole
// command when missing is non-empty and leaves the intent untouched, so a typo
// cannot silently drop the outlets that were named correctly.
func TestRemoveOutlets(t *testing.T) {
	full := []daemonOutlet{{ID: 1, Port: 1081}, {ID: 2, Port: 1082}, {ID: 3, Port: 1083}}
	tests := []struct {
		name        string
		outlets     []daemonOutlet
		ids         map[int]bool
		all         bool
		wantKeep    []int
		wantRemoved []int
		wantMissing []int
	}{
		{
			name: "all removes every outlet", outlets: full, ids: map[int]bool{2: true}, all: true,
			wantKeep: []int{}, wantRemoved: []int{1, 2, 3},
		},
		{
			name: "named ids keep the rest", outlets: full, ids: map[int]bool{1: true, 3: true},
			wantKeep: []int{2}, wantRemoved: []int{1, 3},
		},
		{
			name: "one unknown id is reported as missing", outlets: full, ids: map[int]bool{1: true, 99: true},
			wantKeep: []int{2, 3}, wantRemoved: []int{1}, wantMissing: []int{99},
		},
		{
			name: "all on an empty list", outlets: nil, ids: map[int]bool{}, all: true,
			wantKeep: []int{}, wantRemoved: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keep, removed, missing := removeOutlets(tt.outlets, tt.ids, tt.all)
			if got := idsOf(keep); !reflect.DeepEqual(got, tt.wantKeep) {
				t.Errorf("keep = %v, want %v", got, tt.wantKeep)
			}
			if !reflect.DeepEqual(removed, tt.wantRemoved) {
				t.Errorf("removed = %v, want %v", removed, tt.wantRemoved)
			}
			if !reflect.DeepEqual(missing, tt.wantMissing) {
				t.Errorf("missing = %v, want %v", missing, tt.wantMissing)
			}
		})
	}
}

func idsOf(outlets []daemonOutlet) []int {
	ids := []int{}
	for _, o := range outlets {
		ids = append(ids, o.ID)
	}
	return ids
}
