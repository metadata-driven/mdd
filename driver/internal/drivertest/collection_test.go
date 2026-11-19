package drivertest_test

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/metadata-driven/mdd/driver/file"
	"github.com/metadata-driven/mdd/driver/internal/drivertest"
	_ "github.com/metadata-driven/mdd/driver/json"
	_ "github.com/metadata-driven/mdd/driver/mem"
	_ "github.com/metadata-driven/mdd/driver/mysql"
	_ "github.com/metadata-driven/mdd/driver/yaml"
)

func TestCollectionFilters(t *testing.T) {
	for _, name := range []string{"mem", "file", "json", "yaml", "mysql"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			connection := map[string]string{"mem": "mem:", "file": "file:" + filepath.Join(dir, "files"), "json": filepath.Join(dir, "data.json"), "yaml": filepath.Join(dir, "data.yaml")}[name]
			table := "records"
			if name == "mysql" {
				connection = "mysql://root@/testdb"
				var suffix [8]byte
				if _, err := rand.Read(suffix[:]); err != nil {
					t.Fatal(err)
				}
				table = fmt.Sprintf("mdd_collection_%x", suffix)
				db, err := sql.Open("mysql", strings.TrimPrefix(connection, "mysql://"))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _, _ = db.Exec("DROP TABLE IF EXISTS `" + table + "`"); _ = db.Close() })
			}
			drivertest.CollectionFilters(t, connection, table)
		})
	}
}
