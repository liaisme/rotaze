package cli

import "testing"

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
