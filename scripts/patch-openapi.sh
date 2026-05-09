#!/usr/bin/env bash
# patch-openapi.sh — Apply temporary workarounds to the auto-generated OpenAPI
# spec before running oapi-codegen. The original file is never modified; a
# patched copy is written to the output path and deleted after code generation.
#
# Usage: patch-openapi.sh <input.yaml> <output.yaml>
#
# Known issues (remove patches as they are fixed server-side):
#
# 1. Duplicate operationId "listOauthProtectedResource"
#    Both /.well-known/oauth-protected-resource and
#    /v1/api/.well-known/oauth-protected-resource share the same operationId,
#    causing oapi-codegen to emit duplicate Go declarations.
#    Fix: rename the second occurrence to listOauthProtectedResourceV1.

set -euo pipefail

input="${1:?usage: patch-openapi.sh <input> <output>}"
output="${2:?usage: patch-openapi.sh <input> <output>}"

cp "$input" "$output"

# --- Patch 1: duplicate operationId -------------------------------------------
# The second occurrence (under /v1/api/.well-known/...) is renamed. We match the
# exact context to avoid false positives.
sed -i '/\/v1\/api\/\.well-known\/oauth-protected-resource/,/operationId:/{
  s/operationId: listOauthProtectedResource$/operationId: listOauthProtectedResourceV1/
}' "$output"

echo "patch-openapi.sh: patched $input -> $output"
