# sc-graphql results
> 
> **Status:** This scan was performed 2026-06-26. All findings have been
> reviewed and are **resolved** in the current codebase (v0.8.8, July 2026).
> See [SECURITY-REPORT.md](./SECURITY-REPORT.md) for the full status update
> with per-finding resolution tracking.
>

No issues found by sc-graphql.

## Scan summary

UWAS does not use GraphQL. The codebase exposes its admin API as REST/JSON
over the Go stdlib `net/http` router (`internal/admin/routes.go`).
There is no GraphQL server, schema, resolver, type definition, directive, or
GraphQL dependency in `go.mod`/`go.sum` or `web/dashboard/package.json`.

### Discovery evidence
- `grep -rilE "graphql|graphiql|typeDefs|ApolloServer|makeExecutableSchema|gqlgen|__schema|resolvers"` over all `*.go`, `*.ts`, `*.tsx`, `*.json`, `*.graphql`, `*.gql` files (excluding node_modules) returned only two files, both unrelated to a GraphQL implementation:
  - `internal/middleware/security.go:169` — the WAF lists `application/graphql+json` as a JSON-family content type for body scanning purposes only.
  - `internal/middleware/waf_new_test.go` — a test that verifies WAF behavior for a `application/graphql+json` request body.
- No `*.graphql` / `*.gql` schema files exist.
- No GraphQL libraries are present in dependencies.

### Defenses observed (incidental)
- The WAF (`internal/middleware/security.go`) inspects request bodies including
  JSON and `application/graphql+json` content types, so even if a GraphQL
  endpoint were added later, WAF body scanning would apply.

Because the vulnerability class this skill covers (GraphQL injection,
introspection abuse, query-complexity/depth DoS, batching abuse, field-level
authorization bypass, subscription hijacking) requires a GraphQL surface that
does not exist here, there are no applicable findings.
