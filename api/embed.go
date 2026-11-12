// Package api содержит OpenAPI-спецификацию, которую отдаёт Swagger UI.
package api

import _ "embed"

//go:embed openapi.yaml
var OpenAPI []byte
