// Package openapi exposes the merged OpenAPI v2 spec for all REST
// endpoints. Regenerate with `buf generate` — the file is authoritative,
// never hand-edited.
package openapi

import _ "embed"

//go:embed api.swagger.json
var Spec []byte
