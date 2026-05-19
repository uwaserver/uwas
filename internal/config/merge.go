package config

// DomainPatchFields lists the JSON keys whose presence in the patch
// body indicates the caller intends to apply that field even when the
// parsed value is the Go zero value (empty slice, false, zero number).
// MergeDomain consults this when deciding between "merge: only override
// non-zero" and "explicit: replace whatever the caller sent". The
// admin handler builds this by walking the raw JSON, so a missing key
// means "do not touch the existing value" and an explicit null/empty
// means "clear it".
type DomainPatchFields struct {
	HasAliases     bool
	HasLocations   bool
	HasBasicAuth   bool
	HasSecurity    bool
	HasCache       bool
	HasCompression bool
	HasHtaccess    bool
	HasSSL         bool
	HasSSLForce    bool
	HasResources   bool
}

// MergeDomain produces a merged domain by overlaying patch fields onto
// existing. The two-mode behavior matches the admin PUT contract:
//
//   - replaceMode = false (default): only non-zero patch fields override
//     existing values. Compression must have Enabled or Algorithms set
//     to count as "provided". This preserves operator-edited fields when
//     the dashboard ships partial PUTs.
//
//   - replaceMode = true: cache / security / compression are taken
//     verbatim from patch, even if zeroed, so a caller can disable a
//     feature (e.g. `cache: { enabled: false }`).
//
// The fields field uses presence-of-key detection for collection-shaped
// fields (aliases, locations, basic_auth, htaccess, ssl, resources)
// where Go's zero value is ambiguous with "explicitly empty".
//
// MergeDomain is deliberately pure: it does not validate, look up
// duplicates, or persist. Callers handle those. Refs: refactor.md A23.
func MergeDomain(existing, patch Domain, fields DomainPatchFields, replaceMode bool) Domain {
	merged := existing

	if patch.Host != "" {
		merged.Host = patch.Host
	}
	if patch.Type != "" {
		merged.Type = patch.Type
	}
	if patch.IP != "" {
		merged.IP = patch.IP
	}
	if patch.Root != "" {
		merged.Root = patch.Root
	}

	// SSL is presence-keyed because cert/key/min_version can legitimately
	// be empty strings under Mode = "auto" / "off".
	if fields.HasSSL {
		if patch.SSL.Mode != "" {
			merged.SSL.Mode = patch.SSL.Mode
		}
		if fields.HasSSLForce {
			merged.SSL.ForceSSL = patch.SSL.ForceSSL
		}
		if patch.SSL.Cert != "" {
			merged.SSL.Cert = patch.SSL.Cert
		}
		if patch.SSL.Key != "" {
			merged.SSL.Key = patch.SSL.Key
		}
		if patch.SSL.MinVersion != "" {
			merged.SSL.MinVersion = patch.SSL.MinVersion
		}
	} else {
		// Legacy admin path (no raw inspection): fall back to zero-check on Mode.
		if patch.SSL.Mode != "" {
			merged.SSL.Mode = patch.SSL.Mode
			merged.SSL.ForceSSL = patch.SSL.ForceSSL
			if patch.SSL.Cert != "" {
				merged.SSL.Cert = patch.SSL.Cert
			}
			if patch.SSL.Key != "" {
				merged.SSL.Key = patch.SSL.Key
			}
			if patch.SSL.MinVersion != "" {
				merged.SSL.MinVersion = patch.SSL.MinVersion
			}
		}
	}

	if fields.HasAliases {
		merged.Aliases = patch.Aliases
	}

	// PHP: each subfield gates independently so partial PHP updates work.
	if patch.PHP.FPMAddress != "" {
		merged.PHP.FPMAddress = patch.PHP.FPMAddress
	}
	if len(patch.PHP.IndexFiles) > 0 {
		merged.PHP.IndexFiles = patch.PHP.IndexFiles
	}
	if patch.PHP.MaxUpload > 0 {
		merged.PHP.MaxUpload = patch.PHP.MaxUpload
	}
	if len(patch.PHP.Env) > 0 {
		merged.PHP.Env = patch.PHP.Env
	}

	if len(patch.Proxy.Upstreams) > 0 {
		merged.Proxy = patch.Proxy
	}
	if patch.Redirect.Target != "" {
		merged.Redirect = patch.Redirect
	}
	// App: merge field-by-field instead of wholesale replace. Previously this
	// said `if patch.App.Command != "" || Runtime != "" { merged.App = patch.App }`
	// — which meant a dashboard PUT that only adjusted, say, command silently
	// reset Port back to 0, Env to nil, AutoRestart to false, etc. Operators
	// then saw the YAML's `app:` block shrink or disappear after each edit and
	// the proxy lost track of the running process. Merge each field on its own
	// merit; if the patch field is the zero value, keep what existing had.
	if patch.App.Command != "" {
		merged.App.Command = patch.App.Command
	}
	if patch.App.Runtime != "" {
		merged.App.Runtime = patch.App.Runtime
	}
	if patch.App.Port > 0 {
		merged.App.Port = patch.App.Port
	}
	if patch.App.WorkDir != "" {
		merged.App.WorkDir = patch.App.WorkDir
	}
	if len(patch.App.Env) > 0 {
		merged.App.Env = patch.App.Env
	}
	// Bool fields need separate gating because zero-value (false) can be a
	// legitimate user choice. We can't tell "user set false" from "user didn't
	// touch it" without a tri-state — accept that AutoRestart and Disabled
	// are full-replace in replaceMode only.
	if replaceMode {
		merged.App.AutoRestart = patch.App.AutoRestart
		merged.App.Disabled = patch.App.Disabled
	}
	if fields.HasResources || patch.Resources.CPUPercent > 0 || patch.Resources.MemoryMB > 0 || patch.Resources.PIDMax > 0 {
		merged.Resources = patch.Resources
	}
	if fields.HasHtaccess || patch.Htaccess.Mode != "" {
		merged.Htaccess = patch.Htaccess
	}

	// Locations: replace when the caller explicitly sent the key or
	// requested replace-mode. An empty list clears all routes.
	if fields.HasLocations || replaceMode {
		merged.Locations = patch.Locations
	}
	if fields.HasBasicAuth || replaceMode {
		merged.BasicAuth = patch.BasicAuth
	}

	if replaceMode {
		merged.Cache = patch.Cache
		merged.Security = patch.Security
		merged.Compression = patch.Compression
	} else {
		if fields.HasCache {
			merged.Cache = patch.Cache
		}
		if fields.HasSecurity {
			merged.Security = patch.Security
		}
		if fields.HasCompression || patch.Compression.Enabled || len(patch.Compression.Algorithms) > 0 {
			merged.Compression = patch.Compression
		}
	}

	return merged
}
