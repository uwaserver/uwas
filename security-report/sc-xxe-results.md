# sc-xxe results

No issues found by sc-xxe.

## Scope examined

XXE (CWE-611) requires an XML parser that resolves external entities / DTDs on attacker-influenced input. UWAS is a Go server with a React/TS dashboard.

### XML usage found (all Go `encoding/xml`)
- `internal/backup/s3.go:122` — `xml.NewDecoder(resp.Body).Decode(&result)` parses an S3 `ListBucketResult` response.
- `internal/dnsmanager/route53.go:53` — `xml.Unmarshal(body, &resp)` parses an AWS Route53 API response.
- `internal/dnsmanager/route53.go:92` — `xml.Unmarshal(body, &resp)` parses an AWS Route53 API response.

### Why these are not vulnerable
1. **Parser is inherently safe.** Go's `encoding/xml` does not process DOCTYPE/DTD external entity declarations and does not fetch external resources (`file://`, `http://`). It also does not expand custom internal entities — a custom entity reference produces a parse error rather than expansion — so the classic file-disclosure, SSRF, and billion-laughs vectors do not apply. This is the documented false positive #3 for this skill class.
2. **Input is trusted, not attacker-controlled.** All three decode sites consume HTTPS responses from S3 / Route53 endpoints the operator configured, not request bodies from the public HTTP surface.

### Dashboard (TypeScript/React)
No XML parsing present. `web/dashboard/src` contains only an SVG file-extension check (`FileManager.tsx:183`) and an SVG mention in a settings placeholder (`Settings.tsx:312`). No `DOMParser`, `parseFromString`, `xml2js`, `fast-xml-parser`, or `xmldom`.

### Not present
No SOAP, SVG parsing, XLSX/DOCX ingestion, RSS/Atom feed parsing, or XSLT. The migration converter (`internal/migrate`) parses Apache/Nginx config text, not XML.

## Defenses observed
- Exclusive use of Go's `encoding/xml`, which is XXE-safe by default.
- XML is only parsed from trusted cloud-provider API responses, never from user/HTTP request bodies.
