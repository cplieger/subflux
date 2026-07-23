package schema

import (
	"strconv"
	"strings"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/config/defaults"
)

const (
	defaultFalse = "false"
	showWhenOIDC = "oidc_enabled=true"
)

func arrFields(name, defaultURL string) []api.SchemaField {
	return []api.SchemaField{
		{
			Key: "url", Label: "URL", Type: fieldText,
			Placeholder: defaultURL,
			Help:        "Internal Docker hostname or IP:port for API calls",
			Required:    true,
		},
		{
			Key: "api_key", Label: "API Key", Type: fieldSecret,
			Help:     "From Settings > General in " + name,
			Required: true,
		},
		{
			Key: "public_url", Label: "Public URL", Type: fieldText,
			Placeholder: "https://" + strings.ToLower(name) + ".example.com",
			Help:        "Browser-accessible URL for web UI links (falls back to URL if empty)",
		},
	}
}

func sonarrSection() api.SchemaSection {
	return api.SchemaSection{
		Key: keySonarr, Title: "Sonarr", Type: fieldFields,
		RequiredGroup: groupArr,
		EnableKey:     keyEnabled,
		Fields:        arrFields("Sonarr", "http://sonarr:8989"),
	}
}

func radarrSection() api.SchemaSection {
	return api.SchemaSection{
		Key: "radarr", Title: "Radarr", Type: fieldFields,
		RequiredGroup: groupArr,
		EnableKey:     keyEnabled,
		Fields:        arrFields("Radarr", "http://radarr:7878"),
	}
}

func pollIntervalSection() api.SchemaSection {
	return api.SchemaSection{
		Key: keyPollInterval, Title: "Polling", Type: fieldFields,
		Fields: []api.SchemaField{
			{
				Key: keyPollInterval, Label: "Poll interval", Type: fieldDuration,
				Default:     defaults.FormatDuration(defaults.DefaultPollInterval),
				Placeholder: defaults.FormatDuration(defaults.DefaultPollInterval),
				Min:         defaults.FormatDuration(defaults.MinPollInterval),
				Help:        "How often to check Sonarr/Radarr for new imports (minimum 10s)",
			},
		},
	}
}

func authSection() api.SchemaSection {
	return api.SchemaSection{
		Key:   "auth",
		Title: "Authentication",
		Type:  fieldFields,
		Fields: []api.SchemaField{
			{Key: "basic_enabled", Label: "Password Login", Type: fieldBool, Default: defaultTrue},
			{Key: "oidc_enabled", Label: "OIDC Login", Type: fieldBool, Default: defaultFalse},
			{
				Key:         "oidc.issuer_url",
				Label:       "OIDC Issuer URL",
				Type:        fieldText,
				ShowWhen:    showWhenOIDC,
				Placeholder: "https://authentik.example.com/application/o/subflux/",
			},
			{
				Key:      "oidc.client_id",
				Label:    "OIDC Client ID",
				Type:     fieldText,
				ShowWhen: showWhenOIDC,
			},
			{
				Key:      "oidc.client_secret",
				Label:    "OIDC Client Secret",
				Type:     fieldSecret,
				ShowWhen: showWhenOIDC,
			},
			{
				Key:         "oidc.redirect_uri",
				Label:       "OIDC Redirect URI",
				Type:        fieldText,
				ShowWhen:    showWhenOIDC,
				Placeholder: "https://subflux.example.com/api/auth/oidc/callback",
			},
			{
				Key:      "oidc_auto_redirect",
				Label:    "Auto-redirect to OIDC",
				Type:     fieldBool,
				Default:  defaultFalse,
				ShowWhen: showWhenOIDC,
			},
			{
				Key:     "session_idle_timeout",
				Label:   "Session Idle Timeout",
				Type:    fieldDuration,
				Default: defaults.FormatDuration(defaults.DefaultSessionIdleTimeout),
			},
			{
				Key:     "session_absolute_timeout",
				Label:   "Session Absolute Timeout",
				Type:    fieldDuration,
				Default: defaults.FormatDuration(defaults.DefaultSessionAbsoluteTimeout),
			},
			{
				Key:     "check_breached_passwords",
				Label:   "Check Breached Passwords",
				Type:    fieldBool,
				Default: defaultTrue,
			},
			{
				Key:         "webauthn_rp_id",
				Label:       "WebAuthn RP ID",
				Type:        fieldText,
				Placeholder: "subflux.example.com",
			},
		},
	}
}

func loggingSection() api.SchemaSection {
	return api.SchemaSection{
		Key: "logging", Title: "Logging", Type: fieldFields,
		Fields: []api.SchemaField{
			{
				Key: "level", Label: "Level", Type: fieldSelect,
				Default: defaults.LogLevel,
				Help:    "Log verbosity. Use debug for troubleshooting.",
				Options: []api.SchemaOption{
					{Value: "error", Label: "error"},
					{Value: "warn", Label: "warn"},
					{Value: "info", Label: "info"},
					{Value: "debug", Label: "debug"},
				},
			},
			{
				Key: "format", Label: "Format", Type: fieldSelect,
				Default: defaults.LogFormat,
				Help:    "JSON for log aggregation (Loki/Alloy), text for terminal debugging",
				Options: []api.SchemaOption{
					{Value: "json", Label: "json"},
					{Value: "text", Label: "text"},
				},
			},
		},
	}
}

func mediaRootsSection() api.SchemaSection {
	return api.SchemaSection{
		Key: "media_roots", Title: "Media Roots", Type: fieldList,
		Help: "Directories containing media files. Must match paths inside Sonarr/Radarr containers.",
		Fields: []api.SchemaField{
			{
				Key: "path", Label: "Path", Type: fieldText,
				Placeholder: placeholderMedia,
				Help:        "Absolute path to a media root directory",
			},
		},
	}
}

func trustedProxiesSection() api.SchemaSection {
	return api.SchemaSection{
		Key: "trusted_proxies", Title: "Trusted Proxies", Type: fieldList,
		Help: "CIDR ranges of reverse proxies in front of subflux. When set, the real client IP is " +
			"resolved from a trusted X-Forwarded-For for the audit log, login rate limiter, session " +
			"records, and access log. Leave empty when subflux is directly exposed.",
		Fields: []api.SchemaField{
			{
				Key: "cidr", Label: "Proxy CIDR", Type: fieldText,
				Placeholder: "10.0.0.0/8",
				Help:        "Proxy IP range in CIDR notation; a single proxy is a /32 (e.g. 192.168.1.5/32)",
			},
		},
	}
}

func allowedHostsSection() api.SchemaSection {
	return api.SchemaSection{
		Key: "allowed_hosts", Title: "Allowed Hosts", Type: fieldList,
		Help: "Exact hostnames or IPs subflux answers for. When set, a request whose Host header is " +
			"not listed is rejected with 403, blocking DNS-rebinding attacks against the browser " +
			"session. Requests from localhost (e.g. the container healthcheck) always pass. Leave " +
			"empty to accept any Host.",
		Fields: []api.SchemaField{
			{
				Key: "host", Label: "Hostname or IP", Type: fieldText,
				Placeholder: "subflux.example.com",
				Help:        "Bare hostname or IP, no scheme, path, or port (e.g. subflux.example.com or 192.168.1.5)",
			},
		},
	}
}

func languagesSection() api.SchemaSection {
	return api.SchemaSection{
		Key: keyLanguages, Title: "Languages", Type: fieldLanguages,
		Help: "Audio-to-subtitle language mapping using ISO 639-1 codes.",
	}
}

func backupSection() api.SchemaSection {
	return api.SchemaSection{
		Key:       "backup",
		Title:     "Database Backups",
		Type:      fieldFields,
		EnableKey: keyEnabled,
		Fields: []api.SchemaField{
			{
				Key: "frequency", Label: "Frequency", Type: fieldDuration,
				Default:     defaults.FormatDuration(defaults.DefaultBackupFrequency),
				Placeholder: defaults.FormatDuration(defaults.DefaultBackupFrequency),
				Min:         defaults.FormatDuration(defaults.MinBackupFrequency),
				Help:        "How often to write a consistent database snapshot (minimum 1h).",
			},
			{
				Key: "retention", Label: "Retention", Type: fieldNumber,
				Default: strconv.Itoa(defaults.DefaultBackupRetention),
				Min:     "1",
				Help:    "How many backup files to keep; older ones are pruned.",
			},
			{
				Key: "path", Label: "Backup Directory", Type: fieldText,
				Placeholder: "/config",
				Help:        "Absolute directory for backups. Leave empty to write next to the database.",
			},
		},
	}
}
