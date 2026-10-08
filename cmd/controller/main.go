package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/shui1iao/zeno/internal/controller/api"
)

type handlerConfig struct {
	DBPath                            string
	WebDir                            string
	SeedPreview                       bool
	NodeID                            string
	AgentToken                        string
	AdminToken                        string
	TrustedProxies                    string
	AgentBinaryPath                   string
	AgentVersion                      string
	DisableNotifications              bool
	NotificationAuthorityKey          string
	NotificationCredentialKey         []byte
	NotificationAuthorityActiveKeyID  string
	NotificationAuthorityKeys         map[string]string
	NotificationCredentialActiveKeyID string
	NotificationCredentialKeys        map[string][]byte
}

type controllerRuntime struct {
	Handler http.Handler
	Store   *api.SQLiteStore
	Cleanup func(context.Context) error
}

func buildController(config handlerConfig) (*controllerRuntime, error) {
	if strings.TrimSpace(config.DBPath) == "" {
		return nil, errors.New("-db is required")
	}
	trustedProxies, err := api.ParseTrustedProxies(config.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("parse ZENO_TRUSTED_PROXIES: %w", err)
	}
	backgroundCtx, backgroundCancel := context.WithCancel(context.Background())
	cleanupHandlers := []func(context.Context) error{func(context.Context) error {
		backgroundCancel()
		return nil
	}}
	options := api.HandlerOptions{
		StaticDir: config.WebDir, AgentBinaryPath: config.AgentBinaryPath, AgentVersion: config.AgentVersion,
		BackgroundContext:    backgroundCtx,
		DisableNotifications: config.DisableNotifications,
		TrustedProxies:       trustedProxies,
	}
	if strings.TrimSpace(config.AdminToken) != "" {
		adminPasswordHash, err := api.HashAdminPassword(config.AdminToken)
		if err != nil {
			backgroundCancel()
			return nil, fmt.Errorf("hash admin password: %w", err)
		}
		options.AdminPasswordHash = adminPasswordHash
	}
	var store *api.SQLiteStore
	if config.DBPath != "" {
		opened, err := api.OpenSQLiteStore(config.DBPath)
		if err != nil {
			backgroundCancel()
			return nil, err
		}
		store = opened
		if len(config.NotificationCredentialKeys) > 0 {
			if err := store.ConfigureNotificationCredentialKeyring(context.Background(), config.NotificationCredentialActiveKeyID, config.NotificationCredentialKeys); err != nil {
				_ = store.Close()
				backgroundCancel()
				return nil, err
			}
		} else if len(config.NotificationCredentialKey) > 0 {
			if err := store.ConfigureNotificationCredentialEncryption(context.Background(), config.NotificationCredentialKey); err != nil {
				_ = store.Close()
				backgroundCancel()
				return nil, err
			}
		} else if err := store.RequireNotificationCredentialKeyForExistingCredentials(context.Background()); err != nil {
			_ = store.Close()
			backgroundCancel()
			return nil, err
		}
		var authorized bool
		if len(config.NotificationAuthorityKeys) > 0 {
			authorized, err = store.AuthorizeNotificationAuthorityKeyring(context.Background(), config.NotificationAuthorityActiveKeyID, config.NotificationAuthorityKeys)
		} else {
			authorized, err = store.AuthorizeNotificationAuthority(context.Background(), config.NotificationAuthorityKey)
		}
		if err != nil {
			_ = store.Close()
			backgroundCancel()
			return nil, err
		}
		if !authorized {
			config.DisableNotifications = true
			log.Printf("notification delivery disabled: external authority key is missing or does not match this database")
		}
		options.Store = store
		options.DisableNotifications = config.DisableNotifications
		options.StaleOfflineScanInterval = 5 * time.Second
		options.HistoryRetentionInterval = time.Hour
		options.NotificationReconcileInterval = 2 * time.Second
		options.ExchangeRateRefreshInterval = 24 * time.Hour
		cleanupHandlers = append(cleanupHandlers, func(context.Context) error { return store.Close() })
		if config.SeedPreview {
			if err := store.SeedPreviewData(context.Background(), api.PreviewSeedOptions{NodeID: config.NodeID, DisplayName: "Example Node A", CountryCode: "HK", AgentToken: config.AgentToken}); err != nil {
				_ = store.Close()
				backgroundCancel()
				return nil, err
			}
		}
	}
	handler := api.NewHandler(options)
	if cleanupHandler, ok := handler.(interface{ Cleanup(context.Context) error }); ok {
		cleanupHandlers = append([]func(context.Context) error{cleanupHandler.Cleanup}, cleanupHandlers...)
	}
	cleanup := func(ctx context.Context) error {
		var firstErr error
		for _, cleanupHandler := range cleanupHandlers {
			if err := cleanupHandler(ctx); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}
	return &controllerRuntime{Handler: handler, Store: store, Cleanup: cleanup}, nil
}

const (
	defaultDBCheckTimeout = 10 * time.Minute
	maximumDBCheckTimeout = 24 * time.Hour
)

func validateDBCheckTimeout(timeout time.Duration) error {
	if timeout <= 0 {
		return fmt.Errorf("database check timeout must be greater than zero")
	}
	if timeout > maximumDBCheckTimeout {
		return fmt.Errorf("database check timeout must not exceed %s", maximumDBCheckTimeout)
	}
	return nil
}

func checkSQLiteDatabase(parent context.Context, dbPath string, timeout time.Duration) error {
	if err := validateDBCheckTimeout(timeout); err != nil {
		return err
	}
	store, err := api.OpenSQLiteStore(dbPath)
	if err != nil {
		return err
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	return store.QuickCheck(ctx)
}

func main() {
	options := parseControllerOptions()

	// One-shot maintenance subcommands run instead of serving.
	if options.checkDB {
		if err := runCheckDB(options); err != nil {
			log.Fatal(err)
		}
		return
	}
	if strings.TrimSpace(options.resetAdminPasswordFile) != "" {
		if err := runResetAdminPassword(options); err != nil {
			log.Fatal(err)
		}
		return
	}

	if err := runController(options); err != nil {
		log.Fatal(err)
	}
}

// runController wires the controller and serves until shutdown. It returns an
// error instead of calling log.Fatal so every deferred cleanup registered here
// actually runs; log.Fatal would skip them.
func runController(options *controllerOptions) error {
	secrets, err := resolveControllerSecrets(options)
	if err != nil {
		return err
	}

	runtime, err := buildController(handlerConfig{
		DBPath: options.dbPath, WebDir: options.webDir, SeedPreview: options.seedPreview, NodeID: options.nodeID,
		AgentToken: secrets.agentToken, AdminToken: secrets.adminToken, TrustedProxies: os.Getenv("ZENO_TRUSTED_PROXIES"),
		AgentBinaryPath: options.agentBinaryPath, AgentVersion: options.agentVersion,
		DisableNotifications:             notificationsDisabled(),
		NotificationAuthorityKey:         secrets.authority.key,
		NotificationCredentialKey:        secrets.credentials.key,
		NotificationAuthorityActiveKeyID: secrets.authority.activeKeyID, NotificationAuthorityKeys: secrets.authority.keys,
		NotificationCredentialActiveKeyID: secrets.credentials.activeKeyID, NotificationCredentialKeys: secrets.credentials.keys,
	})
	if err != nil {
		return err
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := runtime.Cleanup(ctx); err != nil {
			log.Printf("controller cleanup timed out or failed: %v", err)
		}
	}()

	if options.collectLocal {
		stopCollector, err := startLocalProbeCollector(runtime, options)
		if err != nil {
			return err
		}
		defer stopCollector()
	}

	logStartupSummary(options)
	return serveHTTP(runtime.Handler, options.addr)
}
