// Command subscription-service is a standalone subscription page and config
// generator for the 3x-ui panel, talking to it exclusively through its
// official REST API. All admin-editable content (settings, app catalog,
// themes, generator templates, per-user template assignments) lives in a
// single SQLite database; only the database's own path is read from a tiny
// bootstrap file.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/irazin/3x-ui-subpage/internal/admin"
	"github.com/irazin/3x-ui-subpage/internal/adminauth"
	"github.com/irazin/3x-ui-subpage/internal/apps"
	"github.com/irazin/3x-ui-subpage/internal/assignment"
	"github.com/irazin/3x-ui-subpage/internal/config"
	"github.com/irazin/3x-ui-subpage/internal/generator/clash"
	"github.com/irazin/3x-ui-subpage/internal/generator/linkgen"
	"github.com/irazin/3x-ui-subpage/internal/generator/mihomo"
	"github.com/irazin/3x-ui-subpage/internal/generator/xrayjson"
	"github.com/irazin/3x-ui-subpage/internal/httpserver"
	"github.com/irazin/3x-ui-subpage/internal/importer"
	"github.com/irazin/3x-ui-subpage/internal/logging"
	"github.com/irazin/3x-ui-subpage/internal/resolver"
	"github.com/irazin/3x-ui-subpage/internal/store"
	"github.com/irazin/3x-ui-subpage/internal/theme"
	"github.com/irazin/3x-ui-subpage/internal/xui"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	configPath := flag.String("config", "bootstrap.yaml", "path to the bootstrap YAML file (only names the SQLite database path)")
	dbPath := flag.String("db", "", "path to the SQLite database (overrides -config's database.path if set)")
	importDir := flag.String("import", "", "seed the database from a web/-shaped directory (applications/themes/templates), then exit")
	createAdminUser := flag.String("create-admin", "", "create or update the admin account with this username, then exit")
	createAdminPassword := flag.String("create-admin-password", "", "password for -create-admin (required if -create-admin is set)")
	flag.Parse()

	resolvedDBPath := *dbPath
	if resolvedDBPath == "" {
		p, err := config.LoadBootstrap(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "subscription-service: %v\n", err)
			os.Exit(1)
		}
		resolvedDBPath = p
	}

	db, err := store.Open(resolvedDBPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "subscription-service: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if *importDir != "" {
		if err := importer.Import(db, *importDir); err != nil {
			fmt.Fprintf(os.Stderr, "subscription-service: import failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("subscription-service: imported %s into %s\n", *importDir, resolvedDBPath)
		return
	}

	if *createAdminUser != "" {
		if *createAdminPassword == "" {
			fmt.Fprintln(os.Stderr, "subscription-service: -create-admin-password is required with -create-admin")
			os.Exit(1)
		}
		if err := adminauth.New(db).CreateUser(*createAdminUser, *createAdminPassword); err != nil {
			fmt.Fprintf(os.Stderr, "subscription-service: create admin failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("subscription-service: admin user %q created/updated\n", *createAdminUser)
		return
	}

	cfg, err := config.LoadFromDB(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "subscription-service: %v\n", err)
		os.Exit(1)
	}

	logger := logging.New(cfg.Logging.Level, cfg.Logging.Format)
	logger.Info("starting subscription-service", "version", version, "listen", cfg.Server.Listen, "db", resolvedDBPath)

	xuiClient, err := xui.New(
		cfg.XUI.BaseURL, cfg.XUI.Username, cfg.XUI.Password,
		cfg.XUI.Timeout, cfg.XUI.Retry.MaxAttempts, cfg.XUI.Retry.Backoff,
		xui.WithLogger(logger),
		xui.WithInsecureSkipVerify(cfg.XUI.InsecureSkipVerify),
	)
	if err != nil {
		logger.Error("failed to build xui client", "err", err)
		os.Exit(1)
	}

	cachedLister := xui.NewCachedLister(xuiClient, cfg.Subscription.CacheTTL)
	sub := resolver.New(cachedLister, cfg.Subscription.ServerHost)

	deps := httpserver.Deps{
		Logger:      logger,
		Resolver:    sub,
		LinkGen:     linkgen.New(db),
		XrayJSON:    xrayjson.New(db),
		Clash:       clash.New(db),
		Mihomo:      mihomo.New(db),
		Theme:       theme.New(db, cfg.Theme.Active),
		ThemeSlug:   cfg.Theme.Active,
		Apps:        apps.New(db),
		Assignments: assignment.New(db),
		QRDefaults:  cfg.QR,
		PublicURL:   cfg.Subscription.PublicURL,
		Support:     cfg.Support,
		Security:    cfg.Security,
	}

	srv := httpserver.New(deps)
	adminSrv := admin.New(db, logger)

	root := chi.NewRouter()
	root.Mount("/", srv.Router())
	root.Mount("/admin", adminSrv.Router())

	httpSrv := &http.Server{
		Addr:         cfg.Server.Listen,
		Handler:      root,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  cfg.Server.IdleTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("listening", "addr", cfg.Server.Listen)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}

	logger.Info("stopped")
}
