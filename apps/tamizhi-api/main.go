package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	core "github.com/vezham-labs/vx-be-kit-lab/packages/tamizhi/core"
)

func main() {
	if err := run(); err != nil {
		slog.Error("Tamizhi stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	configPath := flag.String("config", "config/tamizhi.json", "server configuration path")
	flag.Parse()
	config, err := core.ReadConfig(*configPath)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	dsn, err := config.DatabaseDSN()
	if err != nil {
		return err
	}
	store, err := core.OpenStore(ctx, config.DatabaseDriver, dsn)
	if err != nil {
		return err
	}
	defer store.Close()
	geminiOAuth, err := config.GeminiOAuthOptions()
	if err != nil {
		return err
	}
	runtime, err := core.NewRuntime(ctx, store, core.RuntimeOptions{GeminiOAuth: geminiOAuth, DefaultModel: config.DefaultModel, SystemPrompt: config.SystemPrompt, AllowFullAccess: config.AllowFullAccess})
	if err != nil {
		return err
	}
	defer runtime.Close()
	if err = config.Mount(runtime); err != nil {
		return err
	}
	auth, err := config.NewAuth(ctx, store)
	if err != nil {
		return err
	}
	if err = core.WatchPluginConfig(ctx, *configPath, runtime); err != nil {
		return err
	}
	api := core.NewAPI(runtime, core.HTTPOptions{Origins: config.Origins, Authenticate: auth, AuthRoutes: auth, OIDC: config.Auth.Issuer != ""})
	server := &http.Server{Addr: config.Address, Handler: api, ReadTimeout: 30 * time.Second, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 1 << 16}
	failed := make(chan error, 1)
	go func() {
		slog.Info("Tamizhi API listening", "address", config.Address)
		failed <- server.ListenAndServe()
	}()
	select {
	case err = <-failed:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runtime.Close()
	return server.Shutdown(shutdown)
}
