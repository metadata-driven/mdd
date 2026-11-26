package modelgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateNamedEnums(t *testing.T) {
	dir := t.TempDir()
	source := `
project: {name: example}
enums:
  - name: status
    items: [{name: active}]
  - name: category
    items: []
models:
  - name: user
    enums:
      - name: role
        items: [{name: admin}]
    attrs:
      - {name: status, type: status}
      - {name: history, type: 'status[]'}
      - {name: role, type: user.role}
      - {name: permission, type: enum, items: [{name: read}]}
`
	if err := os.WriteFile(filepath.Join(dir, "meta.yaml"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Generate(dir); err != nil {
		t.Fatal(err)
	}
	for _, file := range []struct {
		name string
		want []string
	}{
		{"enums.go", []string{
			`mdd.Item[Status](Meta, "status.active")`,
			`mdd.RegisterEnum[Category](Meta, "category")`,
			`mdd.Item[UserRole](Meta, "user.role.admin")`,
			`mdd.Item[UserPermission](Meta, "user.permission.read")`,
		}},
		{"user.go", []string{
			`mdd.Attr[*Status](u, "status")`,
			`mdd.Attr[[]*Status](u, "history")`,
			`mdd.Attr[*UserRole](u, "role")`,
		}},
	} {
		data, err := os.ReadFile(filepath.Join(dir, file.name))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range file.want {
			if !strings.Contains(string(data), want) {
				t.Errorf("%s does not contain %s", file.name, want)
			}
		}
	}
}
