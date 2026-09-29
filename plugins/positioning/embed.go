package positioning

import "embed"

// CatalogFS holds the declarative plugin catalog.
//
//go:embed manifest.yaml
var CatalogFS embed.FS
