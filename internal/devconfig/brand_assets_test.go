package devconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentationUsesRepositoryBrandAssets(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, name := range []string{
		"org-logo-v2.svg", "org-logo-mark-v2.svg", "org-logo-mono-v2.svg", "org-logo-favicon-v2.svg",
	} {
		contents, err := os.ReadFile(filepath.Join(root, "assets", "brand", name))
		if err != nil {
			t.Errorf("missing canonical brand asset %s: %v", name, err)
			continue
		}
		if !strings.Contains(string(contents), "<svg") {
			t.Errorf("brand asset %s is not SVG", name)
		}
	}

	readme := read(t, filepath.Join(root, "README.md"))
	if !strings.Contains(readme, `assets/brand/org-logo-mono-v2.svg`) || !strings.Contains(readme, `alt="org"`) {
		t.Error("README.md must lead with the canonical monochrome org logo")
	}
	docs := read(t, filepath.Join(root, "docs", "README.md"))
	if !strings.Contains(docs, `../assets/brand/org-logo-mono-v2.svg`) || !strings.Contains(docs, `alt="org"`) {
		t.Error("docs/README.md must use the canonical monochrome org logo")
	}
}
