package store

import (
	"path/filepath"
	"testing"
)

// A database made before ratings had a language gains the column, and
// opening it again leaves it as it is.
func TestMigrateAddsLanguage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.db")
	s, err := Open(path, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`ALTER TABLE ratings DROP COLUMN language`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	for i := 0; i < 2; i++ {
		s, err := Open(path, []byte("0123456789abcdef0123456789abcdef"))
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		var n int
		if err := s.db.QueryRow(`SELECT count(*) FROM pragma_table_info('ratings') WHERE name = 'language'`).Scan(&n); err != nil || n != 1 {
			t.Fatalf("language columns %d %v", n, err)
		}
		_ = s.Close()
	}
}
