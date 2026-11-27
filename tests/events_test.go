package tests

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/metadata-driven/mdd"
	"github.com/metadata-driven/mdd/driver"
	"github.com/metadata-driven/mdd/driver/mem"
	"github.com/metadata-driven/mdd/meta"
)

type EventApp struct{ mdd.Application[EventContext] }

type EventContext struct {
	mdd.Context
	onNew         func(*EventRecord)
	onDelete      func(*EventRecord)
	onCommit      func(*EventRecord)
	onAfterCommit func(*EventRecord)
	onAfterOnly   func(*AfterOnlyRecord)
}

func (ctx *EventContext) App() *EventApp { return mdd.App[EventApp](ctx) }

type EventRecord struct {
	mdd.Object
	snapshot string
}

type AfterOnlyRecord struct{ mdd.Object }

func (ctx *EventContext) Records() *mdd.Set[EventRecord] { return mdd.NewSet[EventRecord](ctx) }
func (ctx *EventContext) AfterOnlyRecords() *mdd.Set[AfterOnlyRecord] {
	return mdd.NewSet[AfterOnlyRecord](ctx)
}
func (r *EventRecord) Context() *EventContext     { return mdd.Ctx[EventContext](r) }
func (r *AfterOnlyRecord) Context() *EventContext { return mdd.Ctx[EventContext](r) }
func (r *AfterOnlyRecord) OnAfterCommit() {
	if fn := r.Context().onAfterOnly; fn != nil {
		fn(r)
	}
}
func (r *EventRecord) Name() string                     { return mdd.Attr[string](r, "name") }
func (r *EventRecord) SetName(name string) *EventRecord { return mdd.SetAttr(r, "name", name) }
func (r *EventRecord) OnNew() {
	if fn := r.Context().onNew; fn != nil {
		fn(r)
	}
}
func (r *EventRecord) OnDelete() {
	if fn := r.Context().onDelete; fn != nil {
		fn(r)
	}
}
func (r *EventRecord) OnCommit() {
	if fn := r.Context().onCommit; fn != nil {
		fn(r)
	}
}
func (r *EventRecord) OnAfterCommit() {
	if fn := r.Context().onAfterCommit; fn != nil {
		fn(r)
	}
}

func newEventApp(t *testing.T) *EventApp {
	return newEventAppWithSource(t, "mem:")
}

func newEventAppWithSource(t *testing.T, source string) *EventApp {
	t.Helper()
	const config = `
name: event-tests
sources:
  - name: main
    conn: 'mem:'
  - name: secondary
    conn: 'mem:'
models:
  - name: event_record
    source: main
    set: event_records
    attrs:
      - name: name
        type: string
      - name: note
        type: string
      - name: extra
        type: string
        source: secondary
  - name: after_only_record
    source: main
    set: after_only_records
    attrs:
      - name: name
        type: string
`
	cfg, err := meta.ParseYAML([]byte(strings.Replace(config, "mem:", source, 1)))
	if err != nil {
		t.Fatal(err)
	}
	cfg.LookupModel("event_record").Register((*EventRecord)(nil))
	cfg.LookupModel("after_only_record").Register((*AfterOnlyRecord)(nil))
	app, err := mdd.NewApp[EventApp](cfg)
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func TestModelEvents(t *testing.T) {
	app := newEventApp(t)
	var events []string
	var parentID, childID uint64

	err := app.Execute(nil, func(ctx *EventContext) {
		ctx.onNew = func(r *EventRecord) {
			if r.ID() == 0 {
				t.Error("OnNew received an object without an ID")
			}
			events = append(events, "new:"+r.Name())
		}
		ctx.onCommit = func(r *EventRecord) {
			name := r.Name()
			events = append(events, "commit:"+name)
			r.snapshot = name
			if name == "parent" {
				r.SetName("updated")
				childID = ctx.Records().Add().SetName("child").ID()
			} else if name == "child" {
				ctx.Records().Get(parentID).SetName("updated by child")
			}
		}
		ctx.onAfterCommit = func(r *EventRecord) {
			events = append(events, "after:"+r.snapshot)
			if r.ID() == 0 {
				t.Error("OnAfterCommit received an object without an ID")
			}
		}
		parent := ctx.Records().Add().SetName("parent")
		parentID = parent.ID()
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"new:parent", "commit:parent", "new:child", "commit:child", "after:parent", "after:child"}
	if !slices.Equal(events, want) {
		t.Errorf("got events %v, want %v", events, want)
	}
	err = app.Execute(nil, func(ctx *EventContext) {
		assertEqual(t, ctx.Records().Get(parentID).Name(), "updated by child")
		assertEqual(t, ctx.Records().Get(childID).Name(), "child")
	})
	if err != nil {
		t.Fatal(err)
	}

	events = nil
	err = app.Execute(nil, func(ctx *EventContext) {
		ctx.onNew = func(r *EventRecord) { events = append(events, "new") }
		ctx.onCommit = func(r *EventRecord) { events = append(events, "commit:"+r.Name()) }
		ctx.onAfterCommit = func(r *EventRecord) { events = append(events, "after") }
		ctx.Records().Get(parentID).SetName("changed")
	})
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"commit:changed", "after"}
	if !slices.Equal(events, want) {
		t.Errorf("got events %v, want %v", events, want)
	}
}

func TestDeleteEvents(t *testing.T) {
	app := newEventApp(t)
	var deletedID uint64
	err := app.Execute(nil, func(ctx *EventContext) {
		deletedID = ctx.Records().Add().SetName("remove").ID()
		ctx.Records().Add().SetName("keep")
	})
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	err = app.Execute(nil, func(ctx *EventContext) {
		ctx.onDelete = func(r *EventRecord) {
			assertEqual(t, r.Deleted(), true)
			events = append(events, "delete:"+r.Name())
			r.Delete()
			ctx.Records().Add().SetName("audit")
		}
		ctx.onCommit = func(r *EventRecord) { events = append(events, "commit:"+r.Name()) }
		ctx.onAfterCommit = func(r *EventRecord) {
			if r.ID() == deletedID {
				assertEqual(t, r.Deleted(), true)
				assertEqual(t, r.Name(), "")
			}
			events = append(events, fmt.Sprintf("after:%d", r.ID()))
		}
		ctx.Records().Filter("name", "remove").Delete()
		ctx.Records().Get(deletedID).Delete()
		ctx.Records().Get(999).Delete()
		ctx.Records().Add().SetName("unsaved").Delete()
	})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, len(events), 4)
	if len(events) == 4 {
		assertEqual(t, events[0], "delete:")
		assertEqual(t, events[1], "commit:audit")
		if !slices.Contains(events[2:], fmt.Sprintf("after:%d", deletedID)) {
			t.Errorf("missing OnAfterCommit for deleted object: %v", events)
		}
	}
	err = app.Execute(nil, func(ctx *EventContext) {
		assertEqual(t, ctx.Records().Get(deletedID).Deleted(), true)
		assertEqual(t, ctx.Records().Count(), int64(2))
		assertEqual(t, ctx.Records().Filter("name", "audit").Exists(), true)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEventFailureRollsBack(t *testing.T) {
	for _, stage := range []string{"new", "delete", "commit"} {
		t.Run(stage, func(t *testing.T) {
			app := newEventApp(t)
			var id uint64
			if stage == "delete" {
				err := app.Execute(nil, func(ctx *EventContext) { id = ctx.Records().Add().SetName("existing").ID() })
				if err != nil {
					t.Fatal(err)
				}
			}
			failure := errors.New("hook failure")
			after := false
			err := app.Execute(nil, func(ctx *EventContext) {
				fail := func(*EventRecord) { panic(failure) }
				ctx.onAfterCommit = func(*EventRecord) { after = true }
				switch stage {
				case "new":
					ctx.onNew = fail
					ctx.Records().Add().SetName("new").ID()
				case "delete":
					ctx.onDelete = func(r *EventRecord) {
						ctx.Records().Add().SetName("side effect").ID()
						fail(r)
					}
					ctx.Records().Get(id).Delete()
				case "commit":
					ctx.onCommit = fail
					ctx.Records().Add().SetName("new")
				}
			})
			if !errors.Is(err, failure) {
				t.Errorf("got error %v, want hook failure", err)
			}
			assertEqual(t, after, false)
			err = app.Execute(nil, func(ctx *EventContext) {
				want := int64(0)
				if stage == "delete" {
					want = 1
				}
				assertEqual(t, ctx.Records().Count(), want)
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAfterCommitErrors(t *testing.T) {
	app := newEventApp(t)
	first := errors.New("first failure")
	second := errors.New("second failure")
	var called []string
	err := app.Execute(nil, func(ctx *EventContext) {
		ctx.onCommit = func(r *EventRecord) { r.snapshot = r.Name() }
		ctx.onAfterCommit = func(r *EventRecord) {
			assertEqual(t, ctx.Records().Count(), int64(4))
			called = append(called, r.snapshot)
			switch r.snapshot {
			case "first":
				panic(first)
			case "second":
				panic(second)
			case "third":
				panic("third failure")
			}
		}
		ctx.Records().Add().SetName("first")
		ctx.Records().Add().SetName("second")
		ctx.Records().Add().SetName("third")
		ctx.Records().Add().SetName("fourth")
	})
	if err == nil || !errors.Is(err, first) || !errors.Is(err, second) || !strings.Contains(err.Error(), "OnAfterCommit") || !strings.Contains(err.Error(), "third failure") {
		t.Errorf("got error %v, want all after-commit failures", err)
	}
	if !slices.Equal(called, []string{"first", "second", "third", "fourth"}) {
		t.Errorf("got callbacks %v, want all callbacks", called)
	}
	err = app.Execute(nil, func(ctx *EventContext) { assertEqual(t, ctx.Records().Count(), int64(4)) })
	if err != nil {
		t.Fatal(err)
	}
}

func TestAfterCommitRejectsWrites(t *testing.T) {
	for _, tc := range []struct {
		name string
		use  func(*EventContext, *EventRecord)
	}{
		{"write", func(_ *EventContext, r *EventRecord) { r.SetName("invalid") }},
		{"delete", func(_ *EventContext, r *EventRecord) { r.Delete() }},
		{"add", func(ctx *EventContext, _ *EventRecord) { ctx.Records().Add() }},
		{"delete selection", func(ctx *EventContext, _ *EventRecord) { ctx.Records().Delete() }},
		{"commit", func(ctx *EventContext, _ *EventRecord) { ctx.Commit() }},
		{"rollback", func(ctx *EventContext, _ *EventRecord) { ctx.Rollback() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newEventApp(t)
			err := app.Execute(nil, func(ctx *EventContext) {
				ctx.onAfterCommit = func(r *EventRecord) { tc.use(ctx, r) }
				ctx.Records().Add().SetName("saved")
			})
			if err == nil || !strings.Contains(err.Error(), "OnAfterCommit") {
				t.Errorf("got error %v, want OnAfterCommit failure", err)
			}
			err = app.Execute(nil, func(ctx *EventContext) {
				assertEqual(t, ctx.Records().Count(), int64(1))
				assertEqual(t, ctx.Records().First().Name(), "saved")
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAfterCommitCanUseNewExecute(t *testing.T) {
	app := newEventApp(t)
	var nestedErr error
	err := app.Execute(nil, func(ctx *EventContext) {
		ctx.onAfterCommit = func(r *EventRecord) {
			id := r.ID()
			nestedErr = app.Execute(nil, func(next *EventContext) {
				assertEqual(t, next.Records().Get(id).Name(), "saved")
				next.Records().Get(id).SetName("updated")
			})
			assertEqual(t, r.Name(), "saved")
			assertEqual(t, mdd.Attr[string](r, "note"), "")
			assertEqual(t, r.Name(), "saved")
			assertEqual(t, mdd.Attr[string](r, "extra"), "")
			assertEqual(t, ctx.Records().Get(id), r)
			assertEqual(t, ctx.Records().Count(), int64(1))
		}
		ctx.Records().Add().SetName("saved")
	})
	if err != nil || nestedErr != nil {
		t.Fatalf("outer error: %v, nested error: %v", err, nestedErr)
	}
	err = app.Execute(nil, func(ctx *EventContext) { assertEqual(t, ctx.Records().First().Name(), "updated") })
	if err != nil {
		t.Fatal(err)
	}
}

func TestExplicitCommitReportsAfterCommitFailure(t *testing.T) {
	app := newEventApp(t)
	failure := errors.New("after failure")
	err := app.Execute(nil, func(ctx *EventContext) {
		ctx.onAfterCommit = func(*EventRecord) { panic(failure) }
		ctx.Records().Add().SetName("saved")
		ctx.Commit()
	})
	if err == nil || !errors.Is(err, failure) || !strings.Contains(err.Error(), "OnAfterCommit") || strings.Contains(err.Error(), "transaction failed") {
		t.Errorf("got error %v, want after-commit error", err)
	}
	err = app.Execute(nil, func(ctx *EventContext) { assertEqual(t, ctx.Records().Count(), int64(1)) })
	if err != nil {
		t.Fatal(err)
	}
}

func TestOnNewCanChangeTheObject(t *testing.T) {
	app := newEventApp(t)
	var events []string
	var id uint64
	err := app.Execute(nil, func(ctx *EventContext) {
		ctx.onNew = func(r *EventRecord) {
			events = append(events, "new:"+r.Name())
			r.SetName("initialized")
		}
		ctx.onCommit = func(r *EventRecord) { events = append(events, "commit:"+r.Name()) }
		id = ctx.Records().Add().SetName("original").ID()
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(events, []string{"new:original", "commit:initialized"}) {
		t.Errorf("got events %v", events)
	}
	err = app.Execute(nil, func(ctx *EventContext) {
		assertEqual(t, ctx.Records().Get(id).Name(), "initialized")
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAfterCommitTracksOnlyWrittenObjects(t *testing.T) {
	app := newEventApp(t)
	var ids []uint64
	err := app.Execute(nil, func(ctx *EventContext) {
		ctx.onAfterOnly = func(r *AfterOnlyRecord) { ids = append(ids, r.ID()) }
		record := ctx.AfterOnlyRecords().Add()
		record.Set("name", "first")
		record.Set("name", "second")
		id := record.ID()
		ctx.Commit()
		assertIDs(t, ids, []uint64{id})

		record.Set("name", "third")
		ctx.Commit()
		assertIDs(t, ids, []uint64{id, id})

		unsaved := ctx.AfterOnlyRecords().Add()
		unsaved.Set("name", "discarded")
		unsaved.Delete()
		ctx.AfterOnlyRecords().Get(999).Delete()
		ctx.AfterOnlyRecords().Get(id).Get("name")
	})
	if err != nil {
		t.Fatal(err)
	}
	assertEqual(t, len(ids), 2)
}

func TestFailedCommitDoesNotRunAfterCommit(t *testing.T) {
	failure := errors.New("storage commit failed")
	driver.Register(func(schema *driver.Database) (driver.Connection, error) {
		return mem.NewConnection(schema, nil, func(*mem.Snapshot) error { return failure })
	}, "event-failing-storage")
	app := newEventAppWithSource(t, "event-failing-storage:")
	called := false
	err := app.Execute(nil, func(ctx *EventContext) {
		ctx.onAfterCommit = func(*EventRecord) { called = true }
		ctx.Records().Add().SetName("not saved")
	})
	if !errors.Is(err, failure) || strings.Contains(err.Error(), "OnAfterCommit") {
		t.Errorf("got error %v, want storage commit failure", err)
	}
	assertEqual(t, called, false)
	err = app.Execute(nil, func(ctx *EventContext) {
		assertEqual(t, ctx.Records().Count(), int64(0))
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRollbackDiscardsEvents(t *testing.T) {
	app := newEventApp(t)
	var events []string
	err := app.Execute(nil, func(ctx *EventContext) {
		ctx.onNew = func(*EventRecord) { events = append(events, "new") }
		ctx.onCommit = func(*EventRecord) { events = append(events, "commit") }
		ctx.onAfterCommit = func(*EventRecord) { events = append(events, "after") }
		ctx.Records().Add().SetName("discarded").ID()
		ctx.Rollback()
		ctx.Records().Add().SetName("saved")
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(events, []string{"new", "new", "commit", "after"}) {
		t.Errorf("got events %v, want events only for the committed object", events)
	}
	err = app.Execute(nil, func(ctx *EventContext) {
		assertEqual(t, ctx.Records().Count(), int64(1))
		assertEqual(t, ctx.Records().First().Name(), "saved")
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOnCommitRejectsRecursiveTransactionControl(t *testing.T) {
	for _, action := range []string{"commit", "rollback"} {
		t.Run(action, func(t *testing.T) {
			app := newEventApp(t)
			after := false
			err := app.Execute(nil, func(ctx *EventContext) {
				ctx.onCommit = func(*EventRecord) {
					if action == "commit" {
						ctx.Commit()
					} else {
						ctx.Rollback()
					}
				}
				ctx.onAfterCommit = func(*EventRecord) { after = true }
				ctx.Records().Add().SetName("not saved")
			})
			if err == nil {
				t.Error("expected recursive transaction control to fail")
			}
			assertEqual(t, after, false)
			err = app.Execute(nil, func(ctx *EventContext) { assertEqual(t, ctx.Records().Count(), int64(0)) })
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAfterCommitRunsAfterAllSourcesCommit(t *testing.T) {
	app := newEventApp(t)
	var nestedErr error
	called := false
	err := app.Execute(nil, func(ctx *EventContext) {
		ctx.onAfterCommit = func(r *EventRecord) {
			called = true
			nestedErr = app.Execute(nil, func(next *EventContext) {
				saved := next.Records().Get(r.ID())
				assertEqual(t, saved.Name(), "main value")
				assertEqual(t, mdd.Attr[string](saved, "extra"), "secondary value")
			})
		}
		record := ctx.Records().Add().SetName("main value")
		record.Set("extra", "secondary value")
	})
	if err != nil || nestedErr != nil {
		t.Fatalf("outer error: %v, nested error: %v", err, nestedErr)
	}
	assertEqual(t, called, true)
}

func TestSetDeleteCallsEventsForEachObject(t *testing.T) {
	app := newEventApp(t)
	var want []uint64
	err := app.Execute(nil, func(ctx *EventContext) {
		want = append(want, ctx.Records().Add().SetName("remove").ID())
		want = append(want, ctx.Records().Add().SetName("remove").ID())
		ctx.Records().Add().SetName("keep")
	})
	if err != nil {
		t.Fatal(err)
	}
	var deleted, committed []uint64
	err = app.Execute(nil, func(ctx *EventContext) {
		ctx.onDelete = func(r *EventRecord) { deleted = append(deleted, r.ID()) }
		ctx.onAfterCommit = func(r *EventRecord) { committed = append(committed, r.ID()) }
		ctx.Records().Filter("name", "remove").Delete()
	})
	if err != nil {
		t.Fatal(err)
	}
	assertIDs(t, deleted, want)
	assertIDs(t, committed, want)
	err = app.Execute(nil, func(ctx *EventContext) { assertEqual(t, ctx.Records().Count(), int64(1)) })
	if err != nil {
		t.Fatal(err)
	}
}
