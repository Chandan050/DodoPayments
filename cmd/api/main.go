package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"dodo-payments/internal/config"
	"dodo-payments/internal/controller"
	"dodo-payments/internal/logging"
	"dodo-payments/internal/middleware"
	"dodo-payments/internal/payment"
	"dodo-payments/internal/repository"
	"dodo-payments/internal/service"
	"dodo-payments/internal/storage"
)

func main() {
	logger := logging.NewJSON(os.Stdout)
	if err := run(logger); err != nil {
		logger.Error(context.Background(), "invoice service stopped with an error", err)
		os.Exit(1)
	}
}

func run(logger *logging.Logger) error {
	cfg := config.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	db, err := storage.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	defer db.Close()
	if err := storage.Migrate(db, "migrations"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	if err := storage.SeedDemoBusiness(db, cfg.APIKey); err != nil {
		return fmt.Errorf("seed demo business: %w", err)
	}

	repo := repository.New(db)
	psp := payment.NewClient(cfg.PSPURL, 10*time.Second)
	svc := service.New(repo, psp, logger)
	workerContext, stopWorkers := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopWorkers()
	svc.StartWebhookWorker(workerContext)
	auth := middleware.NewAPIKey(repo)
	handler := middleware.RequestLogging(logger, controller.New(svc, auth, logger).Routes())
	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	logger.Info(context.Background(), "invoice service listening", slog.String("address", server.Addr))
	err = server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
