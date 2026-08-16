package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/wjam/speedtest-exporter/internal"
)

func main() {
	// Reminder: `defer` doesn't behave as expected in functions with log.Fatal, os.Exit, etc.
	rootCtx := context.Background()

	var debug bool
	flag.BoolVar(&debug, "debug", false, "show debug logs")

	var interval time.Duration
	flag.DurationVar(&interval, "refresh.interval", 30*time.Minute, "time between refreshes with speedtest")

	var bind string
	flag.StringVar(&bind, "bind", ":9876", "addr to bind the server")

	var server string
	flag.StringVar(&server, "server", "", "speedtest server id")

	flag.Parse()

	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))

	app := app(server, interval)

	if err := runApp(rootCtx, bind, app); err != nil {
		slog.ErrorContext(rootCtx, "failed to run app", "error", err)
		os.Exit(1)
	}
}

var (
	signals            = []os.Signal{syscall.SIGINT, syscall.SIGTERM}
	shutdownPeriod     = 15 * time.Second
	shutdownHardPeriod = 3 * time.Second
	timeSleep          = time.Sleep
)

func app(server string, interval time.Duration) http.Handler {
	reg := prometheus.NewRegistry()
	reg.MustRegister(internal.NewSpeedtestCollectorWithOpts(interval, server))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(
			w, `
			<html>
			<head><title>Speedtest Exporter</title></head>
			<body>
				<h1>Speedtest Exporter</h1>
				<p><a href="/metrics">Metrics</a></p>
			</body>
			</html>
			`,
		)
	})
	mux.Handle("GET /metrics", promhttp.InstrumentMetricHandler(reg, promhttp.HandlerFor(reg, promhttp.HandlerOpts{})))
	return mux
}

func runApp(ctx context.Context, addr string, handler http.Handler) error {
	rootCtx, cancelRoot := signal.NotifyContext(ctx, signals...)
	defer cancelRoot()

	// In-flight requests get a context that won't be immediately cancelled on SIGINT/SIGTERM
	// so that they can be gracefully stopped.
	ongoingCtx, cancelOngoing := context.WithCancel(context.WithoutCancel(rootCtx))
	server := &http.Server{
		Addr: addr,
		BaseContext: func(_ net.Listener) context.Context {
			return ongoingCtx
		},
		Handler:           handler,
		ReadHeaderTimeout: 3 * time.Second,
	}

	errCh := make(chan error)
	go func() {
		defer close(errCh)
		slog.InfoContext(rootCtx, "Server listening", "addr", addr)
		if err := server.ListenAndServe(); err != nil {
			errCh <- err
		}
	}()

	select {
	case <-rootCtx.Done():
		slog.InfoContext(rootCtx, "Received shutdown signal, shutting down")
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			cancelOngoing()
			return err
		}
	}

	slog.InfoContext(rootCtx, "Waiting for ongoing requests to finish")

	shutdownCtx, cancelShutdown := context.WithTimeout(context.WithoutCancel(rootCtx), shutdownPeriod)
	defer cancelShutdown()
	err := server.Shutdown(shutdownCtx)
	cancelOngoing()
	if err != nil {
		slog.ErrorContext(rootCtx, "Failed to wait for ongoing requests to finish, waiting for forced cancellation")
		timeSleep(shutdownHardPeriod)
	}

	slog.InfoContext(rootCtx, "Server shut down")
	return err
}
