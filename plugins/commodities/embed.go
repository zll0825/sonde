package commodities

import "embed"

// CatalogFS holds the declarative plugin catalog (manifest + FRED bindings).
// Gold (Alpha Vantage) stays in the Go collector; FRED series are YAML.
//
//go:embed manifest.yaml bindings.yaml
var CatalogFS embed.FS
