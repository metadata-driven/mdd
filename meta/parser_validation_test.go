package meta

import (
	"strings"
	"testing"
)

func TestParseYAMLRejectsInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, yaml, message string
	}{
		{"invalid YAML", "models: [", "parse metadata YAML"},
		{"duplicate YAML key", "models: []\nmodels: []", "parse metadata YAML"},
		{"root list", "- user", "root must be an object"},
		{"non-string mapping key", "1: value", "mapping keys must be strings"},
		{"models mapping", "models: {user: {}}", "models must be a list"},
		{"missing model name", "models: [{title: User}]", "name must be a nonempty local identifier"},
		{"duplicate model", "models: [{name: user}, {name: user}]", "duplicate models name"},
		{"duplicate attribute", "models: [{name: user, attrs: [{name: login}, {name: login}]}]", "duplicate attrs name"},
		{"duplicate group", "models: [{name: user, attrs: [{group: general}, {group: general}]}]", "duplicate group name"},
		{"duplicate grouped attribute", "models: [{name: user, attrs: [{name: login}, {group: general, attrs: [{name: login}]}]}]", "duplicate attrs name"},
		{"invalid group attributes", "models: [{name: user, attrs: [{group: general, attrs: invalid}]}]", "attrs must be a list"},
		{"duplicate source", "sources: [{name: db}, {name: db}]", "duplicate sources name"},
		{"duplicate model set", "models: [{name: user, set: users}, {name: person, set: users}]", "duplicate set name"},
		{"unknown type", "models: [{name: user, attrs: [{name: owner, type: missing}]}]", "references unknown type"},
		{"unknown domain", "models: [{name: user, domains: [missing]}]", "references unknown or repeated domain"},
		{"unknown attribute domain", "models: [{name: user, attrs: [{name: login, domains: missing}]}]", "user.login references unknown or repeated domain"},
		{"repeated attribute domain", "domains: [{name: shop}]\nmodels: [{name: user, attrs: [{name: login, domains: [shop, shop]}]}]", "user.login references unknown or repeated domain"},
		{"non-string attribute domains", "models: [{name: user, attrs: [{name: login, domains: true}]}]", "user.login.domains must be a string or a list of strings"},
		{"null attribute domains", "models: [{name: user, attrs: [{name: login, domains: null}]}]", "user.login.domains must be a string or a list of strings"},
		{"unknown source", "models: [{name: user, source: missing}]", "references unknown source"},
		{"unknown cache", "models: [{name: user, cache: missing}]", "references unknown source"},
		{"non-string model domains", "models: [{name: user, domains: 42}]", "user.domains must be a string or a list of strings"},
		{"nested models", "models: [{name: user, models: []}]", "models may only be declared at the document root"},
		{"reference without identifier", "models: [{name: user, attrs: [{name: owner, type: person}]}, {name: person}]", "has no scalar identifier"},
		{"collection identifier", "models: [{name: user, set: users, attrs: [{name: id, type: 'string[]'}]}]", "id must have a scalar type"},
		{"TTL without cache", "models: [{name: user, cache_ttl: 10m}]", "cache_ttl requires cache"},
		{"invalid TTL", "sources: [{name: cache}]\nmodels: [{name: user, cache: cache, cache_ttl: soon}]", "non-negative duration"},
		{"items on scalar", "models: [{name: user, attrs: [{name: status, type: string, items: []}]}]", "items is allowed only on enum"},
		{"unknown index attribute", "models: [{name: user, indexes: [{attrs: [missing]}]}]", "references unknown, repeated or queryable attribute"},
		{"empty index", "models: [{name: user, indexes: [{name: empty, attrs: []}]}]", "attrs must not be empty"},
		{"mixed index sources", `
sources: [{name: db}, {name: realtime}]
models:
  - name: user
    source: db
    attrs: [{name: login}, {name: online, type: bool, source: realtime}]
    indexes: [{attrs: [login, online]}]
`, "requires an explicit source"},
		{"unknown item attribute", "models: [{name: user, items: [{name: root, data: {typo: true}}]}]", "unknown attribute"},
		{"invalid enum value", `
models:
  - name: user
    attrs: [{name: status, type: enum, items: [{name: active}]}]
    items: [{name: root, data: {status: missing}}]
`, "not an item of enum"},
		{"invalid scalar value", "models: [{name: user, attrs: [{name: active, type: bool}], items: [{name: root, data: {active: yes_please}}]}]", "incompatible with type"},
		{"invalid collection value", "models: [{name: user, attrs: [{name: tags, type: 'string[]'}], items: [{name: root, data: {tags: scalar}}]}]", "must be a list"},
		{"invalid collection element", "models: [{name: user, attrs: [{name: tags, type: 'string[]'}], items: [{name: root, data: {tags: [valid, 42]}}]}]", "incompatible with type"},
		{"negative ID", "models: [{name: user, set: users, items: [{name: root, id: -1}]}]", "incompatible with type"},
		{"overlong string", "models: [{name: user, attrs: [{name: login, type: 'string(2)'}], items: [{name: root, data: {login: long}}]}]", "incompatible with type"},
		{"invalid template value", "models: [{name: user, to_title: [login]}]", "user.to_title must be a string"},
		{"query on stored value", "models: [{name: user, attrs: [{name: login, query: 'from(users)'}]}]", "query requires a set<T> attribute"},
		{"lookup on scalar", "models: [{name: user, attrs: [{name: login, lookup: 'from(users)'}]}]", "lookup requires a model or enum reference"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseYAML([]byte(tc.yaml))
			require(t, err != nil, "ParseYAML() returned %v, want error", cfg)
			require(t, cfg == nil, "failed ParseYAML() returned a partial configuration")
			require(t, strings.Contains(err.Error(), tc.message), "error = %q, want it to contain %q", err, tc.message)
		})
	}
}

func TestParseYAMLRejectsUnsupportedSyntax(t *testing.T) {
	for _, source := range []string{
		"enums: []",
		"models: [{name: user, kind: model}]",
		"models: [{name: user, domain: shop}]",
		"models: [{name: user, attrs: [{name: status, kind: enum}]}]",
		"models: [{name: user, attrs: [{name: login, len: 25}]}]",
		"models: [{name: user, attrs: [{name: score, type: number, precision: 10, scale: 2}]}]",
		"models: [{name: user, attrs: [{name: roles, type: string, multiple: true}]}]",
	} {
		t.Run(source, func(t *testing.T) {
			cfg, err := ParseYAML([]byte(source))
			require(t, err != nil && cfg == nil, "unsupported syntax accepted: %v, %v", cfg, err)
		})
	}
}

func TestParseYAMLRejectsInvalidTypeExpressions(t *testing.T) {
	for _, expression := range []string{
		"string(-1)", "string(abc)", "string(10,20)",
		"number(0,0)", "number(6,12)", "number(12,-1)", "number(12)", "number(12,2,1)",
		"file(10)", "file[]", "bool(flag)", "string[][]", "[]string", "set<enum>",
		"set<string>", "set<user>[]", "set<user", "user(1)",
	} {
		t.Run(expression, func(t *testing.T) {
			source := "models: [{name: user, attrs: [{name: value, type: '" + expression + "'}]}]"
			cfg, err := ParseYAML([]byte(source))
			require(t, err != nil && cfg == nil, "invalid type %q accepted: %v, %v", expression, cfg, err)
		})
	}
}
