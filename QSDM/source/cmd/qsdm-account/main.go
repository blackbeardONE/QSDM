package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/blackbeardONE/QSDM/pkg/account"
)

func main() {
	logger := log.New(os.Stdout, "qsdm-account ", log.Ldate|log.Ltime|log.LUTC|log.Lmsgprefix)
	cfg, err := account.LoadConfigFromEnv()
	if err != nil {
		logger.Fatalf("configuration rejected: %v", err)
	}
	service, err := account.NewService(cfg, nil, logger)
	if err != nil {
		logger.Fatalf("service initialization failed: %v", err)
	}
	server := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           service.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      25 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	serverError := make(chan error, 1)
	go func() {
		logger.Printf("listening on %s", cfg.ListenAddress)
		serverError <- server.ListenAndServe()
	}()

	shutdownSignal, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-serverError:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("server stopped: %v", err)
		}
		return
	case <-shutdownSignal.Done():
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		logger.Printf("graceful shutdown failed: %v", err)
		_ = server.Close()
	}
}
