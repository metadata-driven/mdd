package tests

import (
	"testing"
	"time"

	"github.com/metadata-driven/mdd"
	"github.com/metadata-driven/mdd/meta"
)

type collectionApp struct {
	mdd.Application[collectionContext]
}
type collectionContext struct{ mdd.Context }

func TestCollectionQueryElementTypes(t *testing.T) {
	cfg := meta.MustParseYAML([]byte(`
name: collections
sources:
  - {name: main, conn: 'mem:'}
models:
  - name: entry
    source: main
    set: entries
    attrs:
      - {name: numbers, type: 'number[]'}
      - {name: ids, type: 'id[]'}
      - {name: times, type: 'datetime[]'}
      - {name: binaries, type: 'binary[]'}
      - {name: labels, type: 'string[]'}
`))
	app, err := mdd.NewApp[collectionApp](cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	moment := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	err = app.Execute(nil, func(ctx *collectionContext) {
		entry := ctx.NewSet("entries").Add()
		entry.Set("numbers", []float64{1.25, 2.5})
		entry.Set("ids", []uint64{12, 112})
		entry.Set("times", []time.Time{moment})
		entry.Set("binaries", [][]byte{[]byte("hello"), {}})
		entry.Set("labels", []string{"", "test"})
		for _, query := range []string{
			"where(numbers &= 1.25, 2.5)",
			"where(ids &= 12, 112)",
			`where(times &= "2025-01-02T03:04:05Z")`,
			`where(binaries &= "hello", "")`,
			`where(labels &= "", test)`,
		} {
			if got := ctx.NewSet("entries").Query(query).Count(); got != 1 {
				t.Errorf("%s: count %d", query, got)
			}
		}
		if got := ctx.NewSet("entries").Where("times", "&=", moment).Count(); got != 1 {
			t.Errorf("time count %d", got)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}
