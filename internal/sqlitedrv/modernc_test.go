//go:build !android

package sqlitedrv

import (
	"strings"
	"testing"
)

func TestDSNCacheSize(t *testing.T) {
	got := dsn("polka.db", Options{CacheSizeKiB: 65536})
	if !strings.Contains(got, "_pragma=cache_size(-65536)") {
		t.Fatalf("dsn = %q, want a 64 MiB cache pragma", got)
	}
	if plain := dsn("polka.db", Options{}); strings.Contains(plain, "cache_size") {
		t.Fatalf("dsn = %q, want no cache pragma by default", plain)
	}
}
