package meta

import (
	"fmt"
	"sync"
	"testing"
)

func TestParseYAMLWarmsCaches(t *testing.T) {
	cfg := parseConfig(t, string(testConfigData))

	assertCached(t, cfg, cacheStore, cacheChildren, cacheTypes, cacheAllTypes, cacheModels, cacheSources)
	assertCached(t, cfg.Lookup("user"), cacheDomains, cacheAttrs, cacheItems, cacheSource)
	assertCached(t, cfg.Lookup("user.login"), cacheType, cacheDomains)
	assertCached(t, cfg.Lookup("user.score"), cacheDomains)
	assertCached(t, cfg.Lookup("user.type"), cacheItems)
	assertCached(t, cfg.Lookup("user.id"), cacheType, cacheSource)
	assertCached(t, cfg.Lookup("user.indexes.login"), cacheAttrs, cacheSource)
	for _, node := range metadataNodes(t, cfg) {
		cacheSize := len(node.cache)
		node.warmCache()
		assertEqual(t, len(node.cache), cacheSize)
	}
}

func TestNodeCachesSlices(t *testing.T) {
	cfg := parseConfig(t, string(testConfigData))
	assertSameSlice(t, cfg.Children(), cfg.Children())
	assertSameSlice(t, cfg.Types(), cfg.Types())
	assertSameSlice(t, cfg.AllTypes(), cfg.AllTypes())
	assertSameSlice(t, cfg.Domains(), cfg.Domains())
	assertSameSlice(t, cfg.Models(), cfg.Models())
	assertSameSlice(t, cfg.Sources(), cfg.Sources())
	for _, model := range cfg.Models() {
		assertSameSlice(t, model.Children(), model.Children())
		assertSameSlice(t, model.Domains(), model.Domains())
		assertSameSlice(t, model.Attrs(), model.Attrs())
		assertSameSlice(t, model.Groups(), model.Groups())
		assertSameSlice(t, model.Items(), model.Items())
		assertSameSlice(t, model.Indexes(), model.Indexes())
		for _, index := range model.Indexes() {
			assertSameSlice(t, index.Attrs(), index.Attrs())
		}
		for _, attr := range model.Attrs() {
			assertSameSlice(t, attr.Domains(), attr.Domains())
			if attr.Kind() == KindEnum {
				enum := attr.(Enum)
				assertSameSlice(t, enum.Items(), enum.Items())
			}
		}
	}

	first := rawValue(cfg, "models.0.items.0.data.permissions").Array()
	second := rawValue(cfg, "models.0.items.0.data.permissions").Array()
	assertSameSlice(t, first, second)
	assertEqual(t, len(first), 3)
	assertEqual(t, rawValue(cfg, "extensions.missing").IsNull(), true)
}

func TestNodeConcurrentReads(t *testing.T) {
	cfg := parseConfig(t, string(testConfigData))
	nodes := metadataNodes(t, cfg)
	user := cfg.Lookup("user")
	models := cfg.Models()
	var readers sync.WaitGroup
	failures := make(chan string, 16)
	for range 16 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for range 20 {
				if rawValue(cfg, "models.0.attrs.2.attrs.0.type").String() != "string(25)" || !rawValue(cfg, "extensions.missing").IsNull() {
					failures <- "raw lookup returned a different value"
					return
				}
				if cfg.Lookup("user") != user || &cfg.Models()[0] != &models[0] {
					failures <- "lookup or slice identity changed"
					return
				}
				for _, node := range nodes {
					if node.Root() != cfg || cfg.Lookup(node.Path()) != node {
						failures <- fmt.Sprintf("metadata identity changed for %s", node.Path())
						return
					}
					node.warmCache()
				}
			}
		}()
	}
	readers.Wait()
	close(failures)
	for message := range failures {
		t.Error(message)
	}
}
