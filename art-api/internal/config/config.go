package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// BuildVersion is injected into release binaries with -ldflags. ART_VERSION and
// RDS_VERSION remain runtime overrides for development and clustered rollouts.
var BuildVersion = "development"

type Config struct {
	ListenAddress                                                       string
	DataDir                                                             string
	DatabaseDriver                                                      string
	DatabaseDSN                                                         string
	JWTSecretFile                                                       string
	InternalSecretFile                                                  string
	BuilderTokenFile                                                    string
	MetricsTokenFile                                                    string
	MFASecretFile                                                       string
	NodeIDFile                                                          string
	NodeID, AdvertiseAddress, Version                                   string
	MFAMode                                                             string
	BootstrapSecretFile                                                 string
	BootstrapUsername                                                   string
	BootstrapPassword                                                   string
	AccessTokenTTL                                                      time.Duration
	SessionTTL                                                          time.Duration
	Issuer                                                              string
	Audience                                                            string
	LoginBurst                                                          int
	LoginWindow                                                         time.Duration
	LoginLockout                                                        time.Duration
	OIDCIssuer                                                          string
	OIDCClientID                                                        string
	OIDCClientSecret                                                    string
	OIDCRedirectURL                                                     string
	OIDCProviderName                                                    string
	OIDCScopes                                                          string
	OIDCAutoRegister                                                    bool
	RegistrationEnabled                                                 bool
	RegistrationAutoApprove                                             bool
	RequireLogin, RequireDeviceDeployment                               bool
	LDAPURL, LDAPBindDN, LDAPBindPassword, LDAPBaseDN, LDAPUserFilter   string
	LDAPUsernameAttribute, LDAPEmailAttribute, LDAPDisplayNameAttribute string
	LDAPGroupAttribute                                                  string
	LDAPGroupMapping                                                    map[string]string
	LDAPStartTLS, LDAPInsecureSkipVerify, LDAPAutoProvision             bool
	WebhookAllowPrivate                                                 bool
	TrustedProxies                                                      []string
	BackupInterval                                                      time.Duration
	BackupRetention                                                     int
}

func Load() (Config, error) {
	dataDir := env("ART_DATA_DIR", "/data")
	cfg := Config{
		ListenAddress:           env("ART_API_LISTEN", ":21114"),
		DataDir:                 dataDir,
		DatabaseDriver:          env("ART_DB_DRIVER", "sqlite"),
		DatabaseDSN:             env("ART_DB_DSN", ""),
		JWTSecretFile:           env("ART_JWT_SECRET_FILE", filepath.Join(dataDir, "secrets", "jwt.secret")),
		InternalSecretFile:      env("ART_INTERNAL_SECRET_FILE", filepath.Join(dataDir, "secrets", "internal.secret")),
		BuilderTokenFile:        env("ART_BUILDER_TOKEN_FILE", filepath.Join(dataDir, "secrets", "builder.token")),
		MetricsTokenFile:        env("ART_METRICS_TOKEN_FILE", filepath.Join(dataDir, "secrets", "metrics.token")),
		MFASecretFile:           env("ART_MFA_SECRET_FILE", filepath.Join(dataDir, "secrets", "mfa.secret")),
		NodeIDFile:              env("ART_NODE_ID_FILE", filepath.Join(dataDir, "secrets", "node.id")),
		NodeID:                  env("ART_NODE_ID", ""),
		AdvertiseAddress:        env("ART_ADVERTISE_ADDRESS", ""),
		Version:                 env("ART_VERSION", BuildVersion),
		MFAMode:                 env("ART_MFA_MODE", "optional"),
		BootstrapSecretFile:     env("ART_BOOTSTRAP_SECRET_FILE", filepath.Join(dataDir, "secrets", "bootstrap-admin.txt")),
		BootstrapUsername:       env("ART_BOOTSTRAP_ADMIN_USERNAME", "admin"),
		BootstrapPassword:       env("ART_BOOTSTRAP_ADMIN_PASSWORD", ""),
		Issuer:                  "art-rustdesk",
		Audience:                "art-hbbs",
		LoginBurst:              envInt("ART_LOGIN_BURST", 5),
		OIDCIssuer:              env("ART_OIDC_ISSUER", ""),
		OIDCClientID:            env("ART_OIDC_CLIENT_ID", ""),
		OIDCClientSecret:        env("ART_OIDC_CLIENT_SECRET", ""),
		OIDCRedirectURL:         env("ART_OIDC_REDIRECT_URL", ""),
		OIDCProviderName:        env("ART_OIDC_PROVIDER_NAME", "oidc"),
		OIDCScopes:              env("ART_OIDC_SCOPES", "openid profile email"),
		OIDCAutoRegister:        envBool("ART_OIDC_AUTO_REGISTER", false),
		RegistrationEnabled:     envBool("ART_REGISTRATION_ENABLED", false),
		RegistrationAutoApprove: envBool("ART_REGISTRATION_AUTO_APPROVE", false),
		RequireLogin:            envBool("ART_REQUIRE_LOGIN", true),
		RequireDeviceDeployment: envBool("ART_REQUIRE_DEVICE_DEPLOYMENT", false),
		WebhookAllowPrivate:     envBool("ART_WEBHOOK_ALLOW_PRIVATE", false),
		TrustedProxies:          splitCSV(env("ART_TRUSTED_PROXIES", "")),
		BackupRetention:         envInt("ART_BACKUP_RETENTION", 14),
		LDAPURL:                 env("ART_LDAP_URL", ""), LDAPBindDN: env("ART_LDAP_BIND_DN", ""), LDAPBindPassword: env("ART_LDAP_BIND_PASSWORD", ""), LDAPBaseDN: env("ART_LDAP_BASE_DN", ""),
		LDAPUserFilter:        env("ART_LDAP_USER_FILTER", "(&(objectClass=person)(|(uid={username})(sAMAccountName={username})(userPrincipalName={username})))"),
		LDAPUsernameAttribute: env("ART_LDAP_USERNAME_ATTRIBUTE", "uid"), LDAPEmailAttribute: env("ART_LDAP_EMAIL_ATTRIBUTE", "mail"), LDAPDisplayNameAttribute: env("ART_LDAP_DISPLAY_NAME_ATTRIBUTE", "displayName"),
		LDAPGroupAttribute: env("ART_LDAP_GROUP_ATTRIBUTE", "memberOf"),
		LDAPStartTLS:       envBool("ART_LDAP_STARTTLS", true), LDAPInsecureSkipVerify: envBool("ART_LDAP_INSECURE_SKIP_VERIFY", false), LDAPAutoProvision: envBool("ART_LDAP_AUTO_PROVISION", false),
	}
	if cfg.DatabaseDSN == "" && cfg.DatabaseDriver == "sqlite" {
		cfg.DatabaseDSN = filepath.Join(dataDir, "art.db")
	}
	var err error
	if cfg.AccessTokenTTL, err = envDuration("ART_ACCESS_TOKEN_TTL", 7*24*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.SessionTTL, err = envDuration("ART_SESSION_TTL", 7*24*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.LoginWindow, err = envDuration("ART_LOGIN_WINDOW", 5*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.LoginLockout, err = envDuration("ART_LOGIN_LOCKOUT", 15*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.BackupInterval, err = envDuration("ART_BACKUP_INTERVAL", 24*time.Hour); err != nil {
		return Config{}, err
	}
	if cfg.BackupInterval < time.Hour {
		return Config{}, fmt.Errorf("ART_BACKUP_INTERVAL must be at least 1h")
	}
	if cfg.BackupRetention < 1 || cfg.BackupRetention > 365 {
		return Config{}, fmt.Errorf("ART_BACKUP_RETENTION must be between 1 and 365")
	}
	if cfg.DatabaseDriver != "sqlite" && cfg.DatabaseDriver != "postgres" {
		return Config{}, fmt.Errorf("ART_DB_DRIVER must be sqlite or postgres")
	}
	if cfg.MFAMode != "optional" && cfg.MFAMode != "required_for_admins" && cfg.MFAMode != "required_for_all_users" {
		return Config{}, fmt.Errorf("ART_MFA_MODE is invalid")
	}
	if cfg.DatabaseDSN == "" {
		return Config{}, fmt.Errorf("ART_DB_DSN is required for %s", cfg.DatabaseDriver)
	}
	configuredOIDC := cfg.OIDCIssuer != "" || cfg.OIDCClientID != "" || cfg.OIDCClientSecret != "" || cfg.OIDCRedirectURL != ""
	if configuredOIDC && (cfg.OIDCIssuer == "" || cfg.OIDCClientID == "" || cfg.OIDCRedirectURL == "") {
		return Config{}, fmt.Errorf("ART_OIDC_ISSUER, ART_OIDC_CLIENT_ID and ART_OIDC_REDIRECT_URL must be configured together")
	}
	if configuredOIDC {
		issuer, issuerErr := url.Parse(cfg.OIDCIssuer)
		if issuerErr != nil || issuer.Scheme != "https" || issuer.Host == "" {
			return Config{}, fmt.Errorf("ART_OIDC_ISSUER must be an absolute HTTPS URL")
		}
		redirect, redirectErr := url.Parse(cfg.OIDCRedirectURL)
		if redirectErr != nil || redirect.Scheme != "https" || redirect.Host == "" {
			return Config{}, fmt.Errorf("ART_OIDC_REDIRECT_URL must be an absolute HTTPS URL")
		}
	}
	configuredLDAP := cfg.LDAPURL != "" || cfg.LDAPBindDN != "" || cfg.LDAPBindPassword != "" || cfg.LDAPBaseDN != ""
	if configuredLDAP && (cfg.LDAPURL == "" || cfg.LDAPBindDN == "" || cfg.LDAPBindPassword == "" || cfg.LDAPBaseDN == "") {
		return Config{}, fmt.Errorf("ART_LDAP_URL, ART_LDAP_BIND_DN, ART_LDAP_BIND_PASSWORD and ART_LDAP_BASE_DN must be configured together")
	}
	if mapping := env("ART_LDAP_GROUP_MAPPING", ""); mapping != "" {
		if err := json.Unmarshal([]byte(mapping), &cfg.LDAPGroupMapping); err != nil {
			return Config{}, fmt.Errorf("ART_LDAP_GROUP_MAPPING must be a JSON object: %w", err)
		}
		for source, target := range cfg.LDAPGroupMapping {
			if source == "" || target == "" {
				return Config{}, fmt.Errorf("ART_LDAP_GROUP_MAPPING keys and values must not be empty")
			}
		}
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if strings.HasPrefix(key, "ART_") {
		if value := os.Getenv("RDS_" + strings.TrimPrefix(key, "ART_")); value != "" {
			return value
		}
	}
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// Value resolves the new RDS_* namespace first and the legacy ART_* alias
// second. It is exported for bootstrap-only secrets used by the API process.
func Value(legacyKey string) string {
	return env(legacyKey, "")
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(env(key, ""))
	if err != nil || value < 1 {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) bool {
	value := env(key, "")
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	value := env(key, "")
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return duration, nil
}

func splitCSV(value string) []string {
	result := make([]string, 0)
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}
