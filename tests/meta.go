package tests

import (
	_ "embed"

	"github.com/metadata-driven/mdd/meta"
)

//go:embed meta.yaml
var metaYAML []byte

var Meta = meta.MustParseYAML(metaYAML)
