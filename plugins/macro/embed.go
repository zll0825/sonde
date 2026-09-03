package macro

import "embed"

// CatalogFS holds the declarative plugin catalog (manifest + FRED bindings).
//
//go:embed manifest.yaml bindings.yaml
var CatalogFS embed.FS
