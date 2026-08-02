package assets

import (
	"embed"
	"io/fs"
)

//go:embed brand/*.svg
var files embed.FS

// ReadBrand returns a versioned, repository-owned brand asset.
func ReadBrand(name string) ([]byte, error) {
	return fs.ReadFile(files, "brand/"+name)
}
