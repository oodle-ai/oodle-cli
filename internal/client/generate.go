package client

//go:generate sh -c "../../scripts/patch-openapi.sh ../../api/openapi.yaml ../../api/openapi.patched.yaml"
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen --config oapi-codegen-types.yaml ../../api/openapi.patched.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen --config oapi-codegen-client.yaml ../../api/openapi.patched.yaml
//go:generate sh -c "rm -f ../../api/openapi.patched.yaml"
