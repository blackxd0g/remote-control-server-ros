package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/art-rustdesk/platform/art-api/internal/audit"
	"github.com/art-rustdesk/platform/art-api/internal/auth"
	"github.com/art-rustdesk/platform/art-api/internal/automation"
	"github.com/art-rustdesk/platform/art-api/internal/backup"
	"github.com/art-rustdesk/platform/art-api/internal/bootstrap"
	"github.com/art-rustdesk/platform/art-api/internal/cluster"
	"github.com/art-rustdesk/platform/art-api/internal/config"
	"github.com/art-rustdesk/platform/art-api/internal/domain"
	"github.com/art-rustdesk/platform/art-api/internal/events"
	"github.com/art-rustdesk/platform/art-api/internal/httpapi"
	"github.com/art-rustdesk/platform/art-api/internal/ldapauth"
	"github.com/art-rustdesk/platform/art-api/internal/managedclient"
	"github.com/art-rustdesk/platform/art-api/internal/mfa"
	"github.com/art-rustdesk/platform/art-api/internal/oidcauth"
	"github.com/art-rustdesk/platform/art-api/internal/relay"
	"github.com/art-rustdesk/platform/art-api/internal/runtimeconfig"
	"github.com/art-rustdesk/platform/art-api/internal/secret"
	"github.com/art-rustdesk/platform/art-api/internal/store/sqlstore"
	"github.com/art-rustdesk/platform/art-api/internal/webhook"
)

func main() {
	if err := run(); err != nil {
		slog.Error("art-api stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	repository, err := sqlstore.Open(cfg.DatabaseDriver, cfg.DatabaseDSN)
	if err != nil {
		return err
	}
	defer repository.Close()
	ctx := context.Background()
	if err := repository.Migrate(ctx); err != nil {
		return err
	}
	bootstrapResult, err := bootstrap.EnsureAdmin(ctx, repository, cfg.BootstrapUsername,
		cfg.BootstrapPassword, cfg.BootstrapSecretFile)
	if err != nil {
		return err
	}
	if bootstrapResult.Created {
		if bootstrapResult.CredentialsFile != "" {
			slog.Warn("bootstrap administrator created; read the generated password and remove the file after first login",
				"credentials_file", bootstrapResult.CredentialsFile)
		} else {
			slog.Info("bootstrap administrator created from environment")
		}
	}
	jwtSecret, err := secret.LoadOrCreate(cfg.JWTSecretFile, config.Value("ART_JWT_SECRET"))
	if err != nil {
		return err
	}
	internalSecret, err := secret.LoadOrCreate(cfg.InternalSecretFile, config.Value("ART_INTERNAL_SECRET"))
	if err != nil {
		return err
	}
	builderToken, err := secret.LoadOrCreate(cfg.BuilderTokenFile, config.Value("ART_BUILDER_TOKEN"))
	if err != nil {
		return err
	}
	metricsToken, err := secret.LoadOrCreate(cfg.MetricsTokenFile, config.Value("ART_METRICS_TOKEN"))
	if err != nil {
		return err
	}
	mfaSecret, err := secret.LoadOrCreate(cfg.MFASecretFile, config.Value("ART_MFA_SECRET"))
	if err != nil {
		return err
	}
	nodeSecret, err := secret.LoadOrCreate(cfg.NodeIDFile, cfg.NodeID)
	if err != nil {
		return err
	}
	nodeDigest := sha256.Sum256(nodeSecret)
	nodeID := "api-" + hex.EncodeToString(nodeDigest[:8])
	mfaService, err := mfa.New(repository, mfaSecret, mfa.Mode(cfg.MFAMode), "Remote Control Server")
	if err != nil {
		return err
	}
	hub := events.NewHub()
	runtimeConfiguration, err := runtimeconfig.New(ctx, repository, runtimeconfig.Values{RequireLogin: cfg.RequireLogin, RequireDeviceDeployment: cfg.RequireDeviceDeployment, RegistrationEnabled: cfg.RegistrationEnabled, RegistrationAutoApprove: cfg.RegistrationAutoApprove, AccessTokenTTL: cfg.AccessTokenTTL, SessionTTL: cfg.SessionTTL, MFAMode: cfg.MFAMode, PasswordMinimumLength: 12, PasswordRequireUpper: true, PasswordRequireLower: true, PasswordRequireNumber: true, PasswordRequireSpecial: true})
	if err != nil {
		return err
	}
	runtimeValues := runtimeConfiguration.Values()
	if err = mfaService.SetMode(mfa.Mode(runtimeValues.MFAMode)); err != nil {
		return err
	}
	tokens := auth.NewTokenManager(jwtSecret, cfg.Issuer, cfg.Audience, runtimeValues.AccessTokenTTL)
	authService, err := auth.NewService(repository, tokens, hub, runtimeValues.SessionTTL)
	if err != nil {
		return err
	}
	authService.SetPasswordPolicy(auth.PasswordPolicy{MinimumLength: runtimeValues.PasswordMinimumLength, RequireUpper: runtimeValues.PasswordRequireUpper, RequireLower: runtimeValues.PasswordRequireLower, RequireNumber: runtimeValues.PasswordRequireNumber, RequireSpecial: runtimeValues.PasswordRequireSpecial})
	if cfg.LDAPURL != "" {
		ldapProvider, providerErr := ldapauth.New(ldapauth.Config{URL: cfg.LDAPURL, BindDN: cfg.LDAPBindDN, BindPassword: cfg.LDAPBindPassword, BaseDN: cfg.LDAPBaseDN, UserFilter: cfg.LDAPUserFilter, UsernameAttribute: cfg.LDAPUsernameAttribute, EmailAttribute: cfg.LDAPEmailAttribute, DisplayNameAttribute: cfg.LDAPDisplayNameAttribute, GroupAttribute: cfg.LDAPGroupAttribute, StartTLS: cfg.LDAPStartTLS, InsecureSkipVerify: cfg.LDAPInsecureSkipVerify})
		if providerErr != nil {
			return providerErr
		}
		authService.AddPasswordProviderWithGroups(ldapProvider, cfg.LDAPAutoProvision, cfg.LDAPGroupMapping)
	}
	monitorContext, stopMonitor := context.WithCancel(context.Background())
	defer stopMonitor()
	clusterCoordinator := cluster.New(repository, domain.ClusterNode{ID: nodeID, Service: "api", Version: cfg.Version, Address: cfg.AdvertiseAddress, StartedAt: time.Now().UTC()})
	go clusterCoordinator.Run(monitorContext)
	relayMonitor := relay.NewMonitor(repository, hub, 30*time.Second, 3*time.Second)
	go clusterCoordinator.RunLeader(monitorContext, "relay-monitor", 20*time.Second, relayMonitor.Run)
	webhookService := webhook.New(repository, hub, internalSecret, cfg.WebhookAllowPrivate)
	go webhookService.RunEvents(monitorContext)
	go clusterCoordinator.RunLeader(monitorContext, "webhook-delivery", 20*time.Second, webhookService.RunDelivery)
	go automation.New(repository, hub).Run(monitorContext)
	var backupService *backup.Service
	if cfg.DatabaseDriver == "sqlite" {
		backupService, err = backup.New(repository, cfg.DataDir, cfg.BackupInterval, cfg.BackupRetention)
		if err != nil {
			return err
		}
		go clusterCoordinator.RunLeader(monitorContext, "backup-scheduler", 30*time.Second, backupService.Run)
	}
	httpServer := httpapi.New(authService, mfaService, audit.NewWithHub(repository, hub), repository, hub, internalSecret,
		httpapi.NewLoginLimiter(cfg.LoginBurst, cfg.LoginWindow, cfg.LoginLockout))
	httpServer.EnableLDAP(cfg.LDAPURL != "", cfg.LDAPAutoProvision)
	httpServer.EnableWebhooks(webhookService)
	httpServer.EnableManagedClients(managedclient.New(repository, internalSecret))
	httpServer.EnableBuilderAPI(builderToken)
	if err := httpServer.EnableOperations(metricsToken, cfg.TrustedProxies); err != nil {
		return err
	}
	httpServer.EnableBranding(cfg.DataDir)
	if backupService != nil {
		httpServer.EnableBackups(backupService)
	}
	httpServer.EnableRuntimeConfig(runtimeConfiguration)
	httpServer.EnableRegistration(cfg.RegistrationEnabled, httpapi.NewLoginLimiter(cfg.LoginBurst, cfg.LoginWindow, cfg.LoginLockout))
	if cfg.OIDCIssuer != "" {
		httpServer.EnableOIDC(oidcauth.New(repository, authService, oidcauth.Config{Issuer: cfg.OIDCIssuer, ClientID: cfg.OIDCClientID, ClientSecret: cfg.OIDCClientSecret, RedirectURL: cfg.OIDCRedirectURL, Name: cfg.OIDCProviderName, Scopes: strings.Fields(cfg.OIDCScopes), AutoRegister: cfg.OIDCAutoRegister}))
	}
	handler := httpServer.Handler()
	server := &http.Server{Addr: cfg.ListenAddress, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second}
	serverErrors := make(chan error, 1)
	go func() {
		slog.Info("art-api listening", "address", cfg.ListenAddress, "database", cfg.DatabaseDriver)
		serverErrors <- server.ListenAndServe()
	}()
	stop, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	select {
	case <-stop.Done():
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		return server.Shutdown(shutdownContext)
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
