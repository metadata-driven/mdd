package driver_test

import (
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/metadata-driven/mdd/driver"
	_ "github.com/metadata-driven/mdd/driver/file"
	"github.com/metadata-driven/mdd/driver/internal/drivertest"
	_ "github.com/metadata-driven/mdd/driver/json"
	_ "github.com/metadata-driven/mdd/driver/mem"
	_ "github.com/metadata-driven/mdd/driver/mysql"
	_ "github.com/metadata-driven/mdd/driver/yaml"
)

var errBinaryRead = errors.New("binary read failed")

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errBinaryRead }

type trackedReader struct {
	io.Reader
	closed *int
}

func (r trackedReader) Close() error {
	*r.closed++
	return nil
}

func TestNewFileInputs(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value any
	}{
		{name: "string", value: "hello"},
		{name: "bytes", value: []byte("hello")},
		{name: "factory", value: func() (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader("hello")), nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value := driver.NewFile(tc.value)
			for range 2 {
				if got := value.String(); got != "hello" {
					t.Fatalf("value = %q", got)
				}
			}
		})
	}

	t.Run("reader", func(t *testing.T) {
		value := driver.NewFile(strings.NewReader("hello"))
		data, err := io.ReadAll(value)
		if err != nil || string(data) != "hello" {
			t.Fatalf("read = %q, %v", data, err)
		}
		if _, err := value.Read(make([]byte, 1)); !errors.Is(err, driver.ErrFileConsumed) {
			t.Fatalf("reopen error = %v", err)
		}
	})

	t.Run("read closer", func(t *testing.T) {
		closed := 0
		value := driver.NewFile(trackedReader{Reader: strings.NewReader("hello"), closed: &closed})
		if err := value.Close(); err != nil || closed != 0 {
			t.Fatalf("close before reading = %v, close count = %d", err, closed)
		}
		buf := make([]byte, 2)
		if n, err := value.Read(buf); n != 2 || err != nil || string(buf) != "he" {
			t.Fatalf("read = %q, %d, %v", buf, n, err)
		}
		if err := value.Close(); err != nil || closed != 1 {
			t.Fatalf("close = %v, close count = %d", err, closed)
		}
		if _, err := value.Read(buf); !errors.Is(err, driver.ErrFileConsumed) {
			t.Fatalf("reopen error = %v", err)
		}
	})

	t.Run("failed reader", func(t *testing.T) {
		value := driver.NewFile(failingReader{})
		buf := make([]byte, 1)
		if _, err := value.Read(buf); !errors.Is(err, errBinaryRead) {
			t.Fatalf("read error = %v", err)
		}
		if _, err := value.Read(buf); !errors.Is(err, driver.ErrFileConsumed) {
			t.Fatalf("reopen error = %v", err)
		}
	})

	if driver.NewFile(nil).Bytes() != nil || driver.NewFile([]byte(nil)).Bytes() != nil {
		t.Fatal("nil binary source is not absent")
	}
	if data := driver.NewFile([]byte{}).Bytes(); data == nil || len(data) != 0 {
		t.Fatalf("empty binary = %#v", data)
	}
	t.Run("unsupported source", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("unsupported source did not panic")
			}
		}()
		driver.NewFile(42)
	})
}

func TestFileStream(t *testing.T) {
	opens, closes := 0, 0
	value := driver.NewFile(func() (io.ReadCloser, error) {
		opens++
		return trackedReader{Reader: strings.NewReader("hello"), closed: &closes}, nil
	})
	if opens != 0 {
		t.Fatal("binary was opened eagerly")
	}
	buf := make([]byte, 2)
	if n, err := value.Read(buf); n != 2 || err != nil || string(buf) != "he" {
		t.Fatalf("first read = %q, %d, %v", buf, n, err)
	}
	if opens != 1 || closes != 0 {
		t.Fatalf("opened %d times, closed %d times", opens, closes)
	}
	if got := value.String(); got != "llo" {
		t.Fatalf("remaining value = %q", got)
	}
	if opens != 1 || closes != 1 {
		t.Fatalf("after EOF: opened %d times, closed %d times", opens, closes)
	}
	if got := value.String(); got != "hello" || opens != 2 || closes != 2 {
		t.Fatalf("reopened value = %q, opens = %d, closes = %d", got, opens, closes)
	}
	if err := value.Close(); err != nil || opens != 2 {
		t.Fatalf("close before reopening = %v, opens = %d", err, opens)
	}
	if n, err := value.Read(buf); n != 2 || err != nil || string(buf) != "he" || opens != 3 {
		t.Fatalf("read after close = %q, %d, %v, opens = %d", buf, n, err, opens)
	}
	if err := value.Close(); err != nil || closes != 3 {
		t.Fatalf("close after reopening = %v, closes = %d", err, closes)
	}

	var attempts int
	retry := driver.NewFile(func() (io.ReadCloser, error) {
		attempts++
		if attempts == 1 {
			return nil, errBinaryRead
		}
		return io.NopCloser(strings.NewReader("ok")), nil
	})
	if _, err := retry.Read(buf); !errors.Is(err, errBinaryRead) {
		t.Fatalf("open error = %v", err)
	}
	if n, err := retry.Read(buf); n != 2 || err != nil || string(buf) != "ok" {
		t.Fatalf("retry = %q, %d, %v", buf, n, err)
	}
	if err := retry.Close(); err != nil {
		t.Fatal(err)
	}

	opens, closes = 0, 0
	readError := driver.NewFile(func() (io.ReadCloser, error) {
		opens++
		if opens == 1 {
			return trackedReader{Reader: failingReader{}, closed: &closes}, nil
		}
		return trackedReader{Reader: strings.NewReader("ok"), closed: &closes}, nil
	})
	if _, err := readError.Read(buf); !errors.Is(err, errBinaryRead) || closes != 1 {
		t.Fatalf("read error = %v, closes = %d", err, closes)
	}
	if n, err := readError.Read(buf); n != 2 || err != nil || string(buf) != "ok" {
		t.Fatalf("read after error = %q, %d, %v", buf, n, err)
	}
	if err := readError.Close(); err != nil || closes != 2 {
		t.Fatalf("close after retry = %v, closes = %d", err, closes)
	}

	absent := driver.NewFile(func() (io.ReadCloser, error) { return nil, nil })
	empty := driver.NewFile(func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("")), nil })
	if absent.Bytes() != nil || empty.Bytes() == nil {
		t.Fatal("absent and empty binary values are not distinguished")
	}
}

func TestFileAndBinaryTransactions(t *testing.T) {
	for _, name := range []string{"mem", "file", "json", "yaml", "mysql"} {
		t.Run(name, func(t *testing.T) {
			testFileAndBinaryTransaction(t, name)
		})
	}
}

func testFileAndBinaryTransaction(t *testing.T, name string) {
	t.Helper()
	dir := t.TempDir()
	connection := map[string]string{
		"mem":  "mem:",
		"file": "file:" + filepath.Join(dir, "files"),
		"json": filepath.Join(dir, "data.json"),
		"yaml": filepath.Join(dir, "data.yaml"),
	}[name]
	table := "records"
	options := drivertest.FileOptions{Reopen: name != "mem"}
	if name == "mysql" {
		connection = "mysql://root@/testdb"
		var suffix [8]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			t.Fatal(err)
		}
		table = fmt.Sprintf("mdd_binary_%x", suffix)
		options.LiveFileReads = true
		options.AfterOpen = func(t *testing.T) {
			checkMySQLBinaryColumns(t, connection, table)
		}
	}
	drivertest.FileAndBinaryTransactions(t, connection, table, options)
}

func checkMySQLBinaryColumns(t *testing.T, connection, table string) {
	t.Helper()
	db, err := sql.Open("mysql", strings.TrimPrefix(connection, "mysql://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := db.Exec("DROP TABLE IF EXISTS `" + table + "`"); err != nil {
			t.Error(err)
		}
		_ = db.Close()
	})
	for column, want := range map[string]string{"raw": "longblob", "first": "longblob"} {
		var got string
		if err := db.QueryRow("SELECT DATA_TYPE FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?", table, column).Scan(&got); err != nil || got != want {
			t.Fatalf("MySQL column %s = %q, %v; want %s", column, got, err, want)
		}
	}
}

func TestBinaryAndFileIndexes(t *testing.T) {
	for _, name := range []string{"mem", "file", "json", "yaml"} {
		for _, typ := range []driver.DataType{driver.TypeBinary, driver.TypeFile} {
			t.Run(fmt.Sprintf("%s/%d", name, typ), func(t *testing.T) {
				dir := t.TempDir()
				connection := map[string]string{"mem": "mem:", "file": "file:" + filepath.Join(dir, "files"), "json": filepath.Join(dir, "data.json"), "yaml": filepath.Join(dir, "data.yaml")}[name]
				schema := &driver.Database{Connection: connection, Tables: []*driver.Table{{Name: "records", Columns: []*driver.Column{{Name: "data", Type: typ}}, Indexes: []*driver.Index{{Name: "by_data", Columns: []string{"data"}}}}}}
				conn, err := driver.NewConnection(schema)
				if typ == driver.TypeFile {
					if err == nil {
						_ = conn.Close()
						t.Fatal("expected unsupported index error")
					}
				} else {
					if err != nil {
						t.Fatal(err)
					}
					_ = conn.Close()
				}
			})
		}
	}
}
