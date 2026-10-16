# OpenAPI specs

The Tenjin API's specs, from which the clients in `../cma` (Campaign Management
API) and `../rma` (Reporting Metrics API) are generated. They are the same specs,
with the same edits, as the Tenjin MCP server's, and are the seed of a public Go
module both can share.

Regenerate with `make generate`. CI fails if the committed clients differ from
what these specs generate.

- `cma.json`: Campaign Management API, generated into `../cma`
- `rma.json`: Reporting Metrics API, generated into `../rma`

The API serves the original specs at `/v2/swagger_doc`.

## Local modifications

The upstream specs declare `openapi: 3.0.2` but contain Swagger-2.0-era
constructs and a few structures that `oapi-codegen` (strict OAS3) cannot
process. The vendored copies here include the minimal edits below. **Edits 1 to 4
are structural / codegen-facing and preserve the HTTP wire contract**: no path,
query/parameter name, JSON property name, or type constraint that affects what
the client sends or receives was changed. Edit 5 corrects a response shape, and
edit 6 adds an endpoint.

1. **Root-level `parameters` → `components.parameters`** (both specs). OAS3
   places reusable parameters under `components`; the originals used the OAS2
   root-level location with `#/parameters/...` refs. Refs updated to
   `#/components/parameters/...`. Parameter `name`/`in` values unchanged.
2. **Unwrapped single-element `links` arrays** (cma.json: `S2sCallback`,
   `CallbackSettings`, `CallbackGroupTemplate`, `CallbackGroup`). `"links":
   [{"$ref": ...}]` → `"links": {"$ref": ...}`. Same property name and target.
3. **Extracted deep sub-property `$ref` targets to named schemas** (cma.json:
   `S2sCallbackData`, `CallbackGroupTemplateData`, `CallbackGroupData`). OAS3
   does not support `$ref`s into `.../properties/data`; the inline schemas were
   lifted to top-level names byte-for-byte.
4. **`x-go-name` on three RMA parameters** (`granularity_daily`,
   `ad_revenue_metrics`, `spend_metrics`) to avoid Go type-name collisions.
   Wire parameter names (`granularity`, `metrics`) are unchanged; only generated
   Go field names differ.
5. **Split list-resource envelopes from the resource object** (cma.json: `App`,
   `Channel`, `Campaign`, `Event`, `AdAccount`). Upstream defined each
   `ArrayOf*.data[]` item as the single-resource *envelope* (`{data, links}`)
   rather than the bare resource (`{id, type, attributes}`), so the generated
   `[]App`/`[]Channel`/`[]Campaign`/`[]Event`/`[]AdAccount` element type had no
   matching fields and decoded every list element to `{}` — silently dropping all
   data. Fixed by making each the bare resource and adding a `<Resource>Response`
   envelope (`{data: <resource>, links}`) for the single-resource endpoints,
   which were repointed accordingly. `Event` has no single-resource endpoint, so
   no `EventResponse` was added. **Unlike edits 1–4, this corrects the modeled
   response shape to match what the API actually returns**: `App`/`Channel`/
   `Campaign` were verified live against `GET /apps`; `Event`/`AdAccount` were
   verified against the API's JSON:API renderer, which emits every
   list element as a bare `{id, type, attributes}` resource identically to `App`.

   The `components.examples` and inline `example` blocks are documentation only:
   oapi-codegen ignores them. Use them as a cross-check of the wire shape, never
   as the source of truth, and keep them in step with the real response.

6. **Added the SDK keys endpoint** (cma.json: `/apps/{app_id}/sdk_keys`,
   `SdkKey`, `ArrayOfSdkKeys`). It was added to the API on 15 October 2026,
   after these specs were taken. The shape follows the API's serializer: type
   `sdk_key`, an integer ID written as a string, and `name`, `key` and
   `created_at` attributes.

When refreshing these specs from upstream, re-apply the equivalent edits (or fix
them upstream) so `oapi-codegen` can run.
