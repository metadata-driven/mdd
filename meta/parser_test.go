package meta

import (
	_ "embed"
	"testing"
)

//go:embed testdata/config.yaml
var testConfigData []byte

func TestParseYAMLCanonical(t *testing.T) {
	cfg := parseConfig(t, string(testConfigData))
	assertNode(t, cfg, KindConfig, "test-app", "$")
	assertEqual(t, cfg.Title(), "My Test App")
	assertEqual(t, cfg.Description(), "Comprehensive metadata configuration example")
	assertEqual(t, cfg.Icon(), "🧩")
	require(t, cfg.Parent() == nil, "root parent = %v, want nil", cfg.Parent())
	assertSameNode(t, cfg.Root(), cfg)
	assertNames(t, cfg.Types(), "id", "bool", "int", "number", "datetime", "string", "binary", "json", "file")
	assertNames(t, cfg.Domains(), "shop", "game")
	assertNames(t, cfg.Models(), "user", "session", "order", "host")
	assertNames(t, cfg.Sources(), "db1", "db2", "realtime", "files", "search", "mem")
	assertNames(t, cfg.AllTypes(), "id", "bool", "int", "number", "datetime", "string", "binary", "json", "file",
		"user", "session", "order", "host", "type", "permissions", "status", "currency")
	assertEqual(t, len(cfg.Children()), 21)
	assertEqual(t, cfg.Types()[0].Title(), "ID")

	user := lookupNodeKind(t, cfg, "user", KindModel)
	assertNode(t, user, KindModel, "user", "user")
	require(t, user.Title() != "" && user.Description() != "" && user.Icon() != "",
		"model presentation strings must be initialized")
	assertNames(t, user.Domains(), "shop", "game")
	assertSameNode(t, user.Domains()[0], cfg.Domains()[0])
	assertNames(t, cfg.Lookup("order").(Model).Domains(), "shop")
	assertNames(t, user.Attrs(), "created", "active", "login", "display_name", "birthday", "score", "avatar",
		"online", "last_seen", "latitude", "longitude", "manager", "best_friends", "tags", "type",
		"previous_type", "permissions", "orders", "recent_orders", "sessions", "id")
	assertNames(t, user.Items(), "super_admin")
	assertNode(t, user.Items()[0], KindItem, "super_admin", "user.super_admin")
	assertEqual(t, user.Items()[0].Data().Get("login").String(), "root")
	assertEqual(t, rawValue(user, "set").String(), "users")
	assertSameNode(t, cfg.Lookup("users"), user)

	metadataNodes(t, cfg)
	for _, path := range []string{"missing", "models.user", "user.attrs.login", "user.items.super_admin"} {
		require(t, cfg.Lookup(path) == nil, "Lookup(%q) = %v, want nil", path, cfg.Lookup(path))
	}
}

func TestParseYAMLAttributeDomains(t *testing.T) {
	cfg := parseConfig(t, string(testConfigData))
	for _, tc := range []struct {
		path    string
		domains []string
	}{
		{"user.created", []string{"shop", "game"}},
		{"user.login", []string{"shop", "game"}},
		{"user.score", []string{"game"}},
		{"user.avatar", []string{"game", "shop"}},
		{"user.tags", nil},
		{"user.type", []string{"shop", "game"}},
		{"user.orders", []string{"shop", "game"}},
		{"session.payload", nil},
		{"order.amount", []string{"shop"}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			attr := cfg.LookupAttr(tc.path)
			domains := attr.Domains()
			assertNames(t, domains, tc.domains...)
			for _, domain := range domains {
				assertSameNode(t, domain, cfg.Lookup("domains."+domain.Name()))
			}
		})
	}
}

func TestParseYAMLTypesAndReferences(t *testing.T) {
	cfg := parseConfig(t, string(testConfigData))
	for _, tc := range []struct {
		path, expression, target, storage string
		collection, set, reference        bool
		length, precision, scale          int
	}{
		{path: "user.login", expression: "string(25)", target: "types.string", storage: "types.string", length: 25},
		{path: "user.display_name", expression: "string", target: "types.string", storage: "types.string"},
		{path: "user.birthday", expression: "datetime", target: "types.datetime", storage: "types.datetime"},
		{path: "user.manager", expression: "user", target: "user", storage: "types.id", reference: true},
		{path: "user.best_friends", expression: "user[]", target: "user", storage: "types.id", collection: true, reference: true},
		{path: "user.tags", expression: "string[]", target: "types.string", storage: "types.string", collection: true},
		{path: "user.type", expression: "user.type", target: "user.type", storage: "types.string", reference: true},
		{path: "user.previous_type", expression: "user.type", target: "user.type", storage: "types.string", reference: true},
		{path: "user.permissions", expression: "user.permissions[]", target: "user.permissions", storage: "types.string", collection: true, reference: true},
		{path: "user.orders", expression: "set<order>", target: "order", set: true, reference: true},
		{path: "session.host", expression: "host", target: "host", storage: "types.id", reference: true},
		{path: "order.amount", expression: "number(12,2)", target: "types.number", storage: "types.number", precision: 12, scale: 2},
		{path: "user.avatar", expression: "file", target: "types.file", storage: "types.file"},
		{path: "session.payload", expression: "binary", target: "types.binary", storage: "types.binary"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			attr := cfg.LookupAttr(tc.path)
			typeInfo := attr.Type()
			assertEqual(t, typeInfo.Expr, tc.expression)
			assertSameNode(t, typeInfo.Target, cfg.Lookup(tc.target))
			var storage Node
			if tc.storage != "" {
				storage = cfg.Lookup(tc.storage)
			}
			assertSameNode(t, typeInfo.Primitive, storage)
			assertEqual(t, typeInfo.Collection, tc.collection)
			assertEqual(t, typeInfo.Set, tc.set)
			assertEqual(t, typeInfo.IsReference(), tc.reference)
			assertEqual(t, typeInfo.Length, tc.length)
			assertEqual(t, typeInfo.Precision, tc.precision)
			assertEqual(t, typeInfo.Scale, tc.scale)
			assertEqual(t, typeInfo.String(), tc.expression)
		})
	}

	for _, name := range []string{"user", "session", "order", "host"} {
		model := lookupNode(t, cfg, name)
		id := findByName(model.Attrs(), "id")
		assertNode(t, id, KindAttr, "id", name+".id")
		assertSameNode(t, id.Type().Target, cfg.Lookup("types.id"))
	}

	userType := cfg.LookupType("user.type").(Enum)
	assertNode(t, userType, KindEnum, "type", "user.type")
	assertNames(t, userType.Items(), "admin", "user", "guest")
	admin := lookupNodeKind(t, cfg, "user.type.admin", KindItem)
	assertSameNode(t, admin, userType.Items()[0])
	assertSameNode(t, admin.Parent(), userType)
	assertEqual(t, admin.Icon(), "🤠")
	assertEqual(t, admin.Source().Conn(), "mem:")
	assertSameNode(t, lookupNode(t, cfg, "order.valid_for_user_type").Type().Target, userType)

	assertEqual(t, lookupNode(t, cfg, "user.orders").Query(), "from(orders) where(user={id}) order(ts)")
	assertEqual(t, lookupNode(t, cfg, "user.recent_orders").Query(), "where(user={id}) order(ts)")
	assertEqual(t, lookupNode(t, cfg, "user.manager").LookupQuery(), "from(users) where(permissions &= manage_users) order(login)")
	assertEqual(t, lookupNode(t, cfg, "user.manager").Query(), "")
}

func TestParseYAMLAttributeGroups(t *testing.T) {
	cfg := parseConfig(t, string(testConfigData))
	user := cfg.Lookup("user").(Model)
	groups := user.Groups()
	assertNames(t, groups, "general", "activity")

	for i, tc := range []struct {
		name, title, icon, description string
		attrs                          []string
	}{
		{"general", "General", "👤", "Basic user information", []string{"login", "display_name", "birthday", "score", "avatar"}},
		{"activity", "Activity", "🟢", "Current status and location", []string{"online", "last_seen", "latitude", "longitude"}},
	} {
		group := groups[i]
		assertNode(t, group, KindGroup, tc.name, "user.groups."+tc.name)
		assertSameNode(t, cfg.Lookup(group.Path()), group)
		assertSameNode(t, group.Parent(), user)
		assertSameNode(t, group.Root(), cfg)
		assertEqual(t, group.Title(), tc.title)
		assertEqual(t, group.Icon(), tc.icon)
		assertEqual(t, group.Description(), tc.description)
		assertNames(t, group.Attrs(), tc.attrs...)
		assertEqual(t, len(group.Children()), 0)
		assertSameSlice(t, group.Attrs(), group.Attrs())
		for _, attr := range group.Attrs() {
			assertSameNode(t, attr, cfg.Lookup("user."+attr.Name()))
			assertSameNode(t, attr.Parent(), user)
			assertSameNode(t, findByName(user.Attrs(), attr.Name()), attr)
		}
	}
	assertEqual(t, groups[0].Options().GetStr("layout"), "compact")
	assertSameSlice(t, groups, user.Groups())
	require(t, cfg.Lookup("user.groups.general.login") == nil, "group must not change attribute paths")
	assertSameNode(t, findByName(user.Attrs(), "id"), cfg.Lookup("user.id"))
	assertEqual(t, len(cfg.Lookup("session").(Model).Groups()), 0)
}

func TestParseYAMLSourcesAndIndexes(t *testing.T) {
	cfg := parseConfig(t, string(testConfigData))
	user := lookupNode(t, cfg, "user")
	assertSameNode(t, user.Source(), cfg.Lookup("sources.db1"))
	assertEqual(t, user.Source().Conn(), "mysql:app@localhost/test_app")
	assertSameNode(t, lookupNode(t, cfg, "user.login").Source(), user.Source())
	assertSameNode(t, lookupNode(t, cfg, "user.online").Source(), cfg.Lookup("sources.realtime"))
	avatar := lookupNode(t, cfg, "user.avatar")
	assertSameNode(t, avatar.Source(), cfg.Lookup("sources.files"))

	assertNames(t, user.Indexes(), "type_active", "location", "search", "login", "type")
	for _, tc := range []struct {
		name, source string
		attrs        []string
	}{
		{"type_active", "db1", []string{"type", "active"}},
		{"location", "realtime", []string{"latitude", "longitude"}},
		{"search", "search", []string{"login", "display_name"}},
		{"login", "db1", []string{"login"}},
		{"type", "db1", []string{"type"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			index := lookupNodeKind(t, cfg, "user.indexes."+tc.name, KindIndex)
			assertNames(t, index.Attrs(), tc.attrs...)
			assertSameNode(t, index.Source(), cfg.Lookup("sources."+tc.source))
			for _, attr := range index.Attrs() {
				assertSameNode(t, attr, cfg.LookupAttr("user."+attr.Name()))
			}
		})
	}
	assertEqual(t, lookupNode(t, cfg, "user.login").IndexType(), IndexTypeRegular)
	assertEqual(t, lookupNode(t, cfg, "user.active").IndexType(), IndexType(""))
}

func TestParseYAMLKeepsRawValuesAndTemplates(t *testing.T) {
	cfg := parseConfig(t, string(testConfigData))
	user := cfg.LookupModel("user")
	assertEqual(t, user.ToTitle(), "{login}")
	assertEqual(t, user.ToIcon(), "{type.icon}")
	assertEqual(t, user.ToDescription(), "{UID} @{login} ({display_name})")
	assertEqual(t, user.Raw().GetStr("to_title"), "{login}")
	assertEqual(t, user.Raw().GetStr("source"), "db1")
	assertEqual(t, cfg.Lookup("user.permissions").Raw().GetStr("type"), "enum[]")
	assertEqual(t, cfg.Lookup("user.recent_orders").Raw().GetStr("query"), "where(user={id}) order(ts)")
	assertEqual(t, rawValue(cfg, "models.0.attrs.2.attrs.0.type").String(), "string(25)")
	assertEqual(t, rawValue(cfg, "models.0.attrs.7.items.0.icon").String(), "🤠")
	assertEqual(t, rawValue(cfg, "models.0.items.0.data.permissions.1").String(), "manage_orders")
	assertEqual(t, len(user.Raw().GetArr("attrs")), 13)
	assertEqual(t, len(user.Raw().GetArr("indexes")), 3)
	assertEqual(t, len(cfg.Raw().GetArr("sources")), 5)
	assertEqual(t, rawValue(cfg, "models.0.attrs.13").IsNull(), true)
	assertEqual(t, rawValue(cfg, "sources.6").IsNull(), true)
}

func TestParseYAMLDefaultsAndForwardReferences(t *testing.T) {
	cfg := parseConfig(t, `
models:
  - name: note
    set: notes
    attrs:
      - name: body
      - name: owner
        type: person
      - name: peers
        type: note[]
  - name: person
    set: people
    attrs:
      - name: last_note
        type: note
  - name: summary
    attrs:
      - name: amount
        type: number
`)
	assertEqual(t, cfg.Name(), "project")
	note := lookupNode(t, cfg, "note")
	assertEqual(t, note.Title(), "note")
	require(t, note.Icon() == "" && note.Description() == "", "missing presentation strings must stay empty")
	require(t, note.Source() == nil, "a model without source must not select an arbitrary provider")
	assertEqual(t, len(cfg.Sources()), 0)
	assertEqual(t, lookupNode(t, cfg, "note.body").Type().Expr, "string")
	assertEqual(t, lookupNode(t, cfg, "note.body").Type().Length, 0)
	assertSameNode(t, lookupNode(t, cfg, "note.owner").Type().Target, cfg.Lookup("person"))
	assertEqual(t, lookupNode(t, cfg, "note.owner").Type().Primitive.Name(), TypeID)
	assertSameNode(t, lookupNode(t, cfg, "note.peers").Type().Target, note)
	assertSameNode(t, lookupNode(t, cfg, "person.last_note").Type().Target, note)
	assertEqual(t, lookupNode(t, cfg, "person.last_note").Type().Primitive.Name(), TypeID)
	assertNames(t, lookupNode(t, cfg, "person").Attrs(), "last_note", "id")
	require(t, cfg.Lookup("summary.id") == nil, "a model without a set must not receive an implicit ID")
	assertEqual(t, lookupNode(t, cfg, "summary.amount").Type().Precision, 0)
	assertEqual(t, lookupNode(t, cfg, "summary.amount").Type().Scale, 0)
}

func TestParseYAMLAllowsCustomMetadata(t *testing.T) {
	cfg := parseConfig(t, `
extensions:
  audit:
    enabled: true
models:
  - name: note
    set: notes
    attrs:
      - name: body
        type: string(100)
        widget:
          name: textarea
          options: [compact, expanded]
    items:
      - name: welcome
        id: 0
        data:
          body: Hello
`)
	assertEqual(t, rawValue(cfg, "extensions.audit.enabled").Bool(), true)
	body := lookupNode(t, cfg, "note.body")
	assertEqual(t, rawValue(body, "widget.name").String(), "textarea")
	assertEqual(t, rawValue(body, "widget.options.1").String(), "expanded")
	assertEqual(t, body.Type().Length, 100)
	assertEqual(t, lookupNode(t, cfg, "note.welcome").ItemID(), any(0))
}

func TestParseYAMLNamedEnums(t *testing.T) {
	cfg := parseConfig(t, `
models:
  - name: order
    attrs:
      - {name: status, type: status}
      - {name: history, type: 'status[]'}
      - {name: valid_for, type: user.type}
    items:
      - name: example
        data:
          status: done
          history: [status.created, done]
          valid_for: user.type.admin

  - name: user
    enums:
      - name: type
        title: Account types
        description: Account categories
        icon: account
        options: {list: compact}
        items:
          - {name: admin, id: 10, title: Administrator}

    attrs:
      - {name: type, type: user.type, title: Current type}
      - name: permissions
        type: enum[]
        items: [{name: read}]

enums:
  - name: status
    items: [{name: created}, {name: done, id: 10}]
`)
	assertNames(t, cfg.Enums(), "status", "type", "permissions")
	status := cfg.LookupEnum("status")
	assertNode(t, status, KindEnum, "status", "status")
	assertSameNode(t, status.Parent(), cfg)
	assertSameNode(t, cfg.LookupType("status"), status)
	assertNames(t, status.Items(), "created", "done")
	assertEqual(t, status.Items()[0].ItemID(), any(uint64(1)))
	assertEqual(t, status.Items()[1].ItemID(), any(10))
	assertSameNode(t, cfg.Lookup("status.done"), status.Items()[1])
	assertEqual(t, status.Items()[1].Source().Conn(), "mem:")
	assertSameNode(t, cfg.LookupAttr("order.status").Type().Target, status)
	history := cfg.LookupAttr("order.history").Type()
	assertSameNode(t, history.Target, status)
	assertEqual(t, history.Collection, true)

	accountType := cfg.LookupEnum("user.type")
	assertNode(t, accountType, KindEnum, "type", "user.type")
	assertSameNode(t, accountType.Parent(), cfg.LookupModel("user"))
	assertSameNode(t, cfg.Lookup("user.type"), accountType)
	assertSameNode(t, cfg.LookupAttr("order.valid_for").Type().Target, accountType)
	assertSameNode(t, cfg.Lookup("user.type.admin"), accountType.Items()[0])
	assertEqual(t, accountType.Title(), "Account types")
	assertEqual(t, accountType.Description(), "Account categories")
	assertEqual(t, accountType.Icon(), "account")
	assertEqual(t, accountType.Options().GetStr("list"), "compact")
	typeAttr := cfg.LookupAttr("user.type")
	assertNode(t, typeAttr, KindAttr, "type", "user.type")
	assertSameNode(t, typeAttr.Type().Target, accountType)
	assertEqual(t, typeAttr.Title(), "Current type")
	permissions := cfg.LookupAttr("user.permissions").Type()
	assertEqual(t, permissions.Expr, "user.permissions[]")
	assertSameNode(t, permissions.Target, cfg.LookupEnum("user.permissions"))
	metadataNodes(t, cfg)
}

func TestParseYAMLMemorySourceForEnumItems(t *testing.T) {
	for _, tc := range []struct {
		name, sources, sourceName string
		sourceCount               int
	}{
		{"generated", "[]", "mem", 1},
		{"existing memory provider", "[{name: memory, conn: 'mem:'}]", "memory", 1},
		{"occupied mem name", "[{name: mem, conn: 'mysql:app/db'}]", "enum_mem", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := parseConfig(t, `
models:
  - name: user
    attrs:
      - name: status
        type: enum
        items:
          - name: set
sources: `+tc.sources)
			status := cfg.LookupType("user.status").(Enum)
			assertNode(t, cfg.Lookup("user.status.set"), KindItem, "set", "user.status.set")
			require(t, cfg.Lookup("set<user.status>") == nil, "enum created a set metadata node")
			assertEqual(t, status.Items()[0].Source().Name(), tc.sourceName)
			assertEqual(t, status.Items()[0].Source().Conn(), "mem:")
			assertEqual(t, len(cfg.Sources()), tc.sourceCount)
		})
	}
}
