package i18n

import (
	"encoding/json"
	"io/fs"
	"path"
	"testing"
)

// TestCatalogFileConflicts verifies that the same Portuguese source does not
// receive different translations in different catalog files of the same
// language.
func TestCatalogFileConflicts(t *testing.T) {
	err := fs.WalkDir(catalogFS, "catalog", func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || path.Ext(filePath) != ".json" {
			return nil
		}
		lang := Language(path.Base(path.Dir(filePath)))
		data, readErr := catalogFS.ReadFile(filePath)
		if readErr != nil {
			return readErr
		}
		var messages map[string]string
		if unmarshalErr := json.Unmarshal(data, &messages); unmarshalErr != nil {
			return unmarshalErr
		}
		for source, translation := range messages {
			if existing, ok := catalogs[lang][source]; ok && existing != translation {
				t.Errorf("%s: %q has conflicting %s translations: %q vs %q", filePath, source, lang, existing, translation)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
