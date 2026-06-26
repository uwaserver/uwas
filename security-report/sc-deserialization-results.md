# sc-deserialization results

No issues found by sc-deserialization.

## Summary

The UWAS codebase contains no insecure deserialization (CWE-502) vulnerabilities. It is a pure-Go server (plus a React/TypeScript dashboard). None of the deserialization formats that enable arbitrary object instantiation / RCE are present.

## Scan coverage

Searched the full repository for every dangerous primitive enumerated in the skill, across Go, TypeScript, PHP, Java, and .NET:

- Go: `encoding/gob`, `yaml.Unmarshal`/`yaml.NewDecoder`, `json.Unmarshal`/`json.NewDecoder`, `xml.Unmarshal`/`xml.NewDecoder`.
- PHP: `unserialize(`, `phar://`, `maybe_unserialize` — none found (no PHP shipped in the binary).
- Node/TS: `node-serialize`, `funcster`, `cryo`, `js-yaml`/`jsyaml`, `eval(`, `new Function(` — none found.
- Java/.NET: `ObjectInputStream`, `readObject`, `BinaryFormatter`, `TypeNameHandling`, `enableDefaultTyping` — none found.

## What is actually used (and why it is safe)

1. **YAML — `gopkg.in/yaml.v3`** (config loader, domain CRUD, settings, software store, PHP per-domain config).
   Sinks: `internal/config/loader.go:48,182,188`, `internal/admin/handlers_domain.go:1354`, `internal/admin/handlers_settings.go:303`, `internal/admin/handlers_php.go:566`, `internal/admin/handlers_software_store.go:58`, `internal/admin/handlers_software_docker.go:427`, `internal/admin/api.go:1437`, `internal/cli/install.go:846`, `internal/apps/store.go:318`.
   Unlike Python PyYAML or Ruby Psych, Go's yaml.v3 maps documents only into Go structs/maps/scalars supplied by the caller. It cannot instantiate arbitrary types or invoke constructors/magic methods, so there is no object-injection / gadget-chain path. yaml.v3 also caps alias expansion, mitigating billion-laughs DoS. No RCE-capable deserialization here.

2. **JSON — `encoding/json`** (151 call sites across HTTP handlers).
   Standard library JSON is a data-only format with no code-execution capability. Unmarshal targets are concrete Go types; even `interface{}` targets yield only `map`/`slice`/scalar values, never executable objects.

3. **XML — `encoding/xml`** (`internal/backup/s3.go:122`, `internal/dnsmanager/route53.go:53,92`).
   Used to parse responses from trusted upstream APIs (S3, AWS Route53). Go's `encoding/xml` does not resolve external entities or DTDs, so neither XXE nor type-confusion deserialization applies. (Source data is also remote-API responses, not direct attacker input.)

## Defenses observed

- Minimal dependency surface (5 direct deps: brotli, quic-go, x/crypto, x/sync, yaml.v3) — none are deserialization-RCE vectors.
- No reflective/polymorphic type resolution (no registered-type maps, no `TypeNameHandling`-style dynamic dispatch).
- Config files containing secrets are written 0600 and parsed into fixed structs.

## Conclusion

No reachable CWE-502 condition exists. The serialization formats in use (Go yaml.v3, encoding/json, encoding/xml) are all data-only and incapable of arbitrary object instantiation or code execution.
