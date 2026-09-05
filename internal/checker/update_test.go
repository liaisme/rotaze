package checker

import (
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/rotaze/rotaze/internal/cache"
)

func TestChecker_UpdateCountry(t *testing.T) {
	dbPath := t.TempDir() + "/uc.db"
	c, err := cache.NewCache(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })

	ch := New(c, zap.NewNop(), &Config{URL: "http://example.com", Country: "VN"})
	ch.UpdateCountry("SG")
	if ch.country != "SG" {
		t.Errorf("country = %q, want SG", ch.country)
	}
}

func TestChecker_UpdateURL(t *testing.T) {
	dbPath := t.TempDir() + "/uu.db"
	c, err := cache.NewCache(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })

	ch := New(c, zap.NewNop(), &Config{URL: "http://old.example.com"})
	ch.UpdateURL("http://new.example.com")
	if ch.url != "http://new.example.com" {
		t.Errorf("url = %q, want new", ch.url)
	}
}

func TestChecker_UpdateSetters_Concurrent(t *testing.T) {
	dbPath := t.TempDir() + "/conc.db"
	c, err := cache.NewCache(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })

	ch := New(c, zap.NewNop(), &Config{URL: "http://example.com", Interval: time.Hour})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			ch.UpdateInterval(time.Duration(i) * time.Second)
			ch.UpdateCountry("VU")
			ch.UpdateURL("http://x.example.com")
		}
	}()
	// Reader side: lock-protected reads, no race should fire
	for i := 0; i < 200; i++ {
		ch.mu.Lock()
		_ = ch.interval
		ch.mu.Unlock()
	}
	<-done
}
