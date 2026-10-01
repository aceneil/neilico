package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"umpp/control-plane/internal/api"
	"umpp/control-plane/internal/auth"
	"umpp/control-plane/internal/config"
	"umpp/control-plane/internal/db"
	"umpp/control-plane/internal/metrics"
	"umpp/control-plane/internal/service"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "umpp-api:", err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("umpp-api", flag.ContinueOnError)
	configPath := flags.String("config", "configs/config.example.yaml", "path to YAML configuration")
	showVersion := flags.Bool("version", false, "print version and exit")
	if err := flags.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *showVersion {
		fmt.Println(version)
		return nil
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	logger := newLogger(cfg.Log)
	handle, err := db.Open(cfg.Database, cfg.Log.Level)
	if err != nil {
		return err
	}
	if err := db.AutoMigrate(handle); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := service.Bootstrap(ctx, handle, cfg.Bootstrap); err != nil {
		return err
	}
	manager, err := auth.NewManager(cfg.Auth.JWTSecret, cfg.Auth.AccessTTL, cfg.Auth.RefreshTTL)
	if err != nil {
		return err
	}
	nodeService := service.NewNodeService(handle, cfg.Node.HeartbeatTimeout)
	sweeper := service.NewNodeSweeper(handle, cfg.Node.HeartbeatTimeout, logger)
	go sweeper.Start(ctx, 15*time.Second)

	promMetrics := metrics.New(handle)
	handler := api.New(handle, manager, nodeService, promMetrics, logger, version)
	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("UMPP API listening", "address", server.Addr, "version", version)
		serverErrors <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func newLogger(cfg config.Log) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(cfg.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	options := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if cfg.Format == "text" {
		handler = slog.NewTextHandler(os.Stdout, options)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, options)
	}
	return slog.New(handler)
}
