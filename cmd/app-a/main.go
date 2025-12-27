package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	otelSetup "go-observability-lab/internal/otel"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

const serviceName = "app-a"

var tracer = otel.Tracer(serviceName)

func main() {
	if err := run(); err != nil {
		log.Fatalln(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Configura OpenTelemetry
	otlpEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if otlpEndpoint == "" {
		otlpEndpoint = "localhost:4317"
	}

	otelShutdown, err := otelSetup.SetupOTelSDK(ctx, serviceName, otlpEndpoint)

	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, otelShutdown(context.Background()))
	}()

	// Servidor HTTP
	srv := &http.Server{
		Addr:         ":8080",
		BaseContext:  func(_ net.Listener) context.Context { return ctx },
		ReadTimeout:  time.Second,
		WriteTimeout: 10 * time.Second,
		Handler:      newHTTPHandler(),
	}

	srvErr := make(chan error, 1)
	go func() {
		log.Printf("🚀 %s iniciado na porta 8080", serviceName)
		srvErr <- srv.ListenAndServe()
	}()

	select {
	case err = <-srvErr:
		return err
	case <-ctx.Done():
		stop()
	}

	err = srv.Shutdown(context.Background())
	return err
}

func newHTTPHandler() http.Handler {
	mux := http.NewServeMux()

	handleFunc := func(pattern string, handlerFunc func(http.ResponseWriter, *http.Request)) {
		handler := otelhttp.WithRouteTag(pattern, http.HandlerFunc(handlerFunc))
		mux.Handle(pattern, handler)
	}

	handleFunc("/", handleRoot)
	handleFunc("/health", handleHealth)

	return otelhttp.NewHandler(mux, "/")
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "handleRoot")
	defer span.End()

	span.SetAttributes(
		attribute.String("http.method", r.Method),
		attribute.String("http.path", r.URL.Path),
	)

	log.Printf("[%s] Recebida requisição em /", serviceName)

	// Chama App B
	appBURL := os.Getenv("APP_B_URL")
	if appBURL == "" {
		appBURL = "http://localhost:8081"
	}

	result, err := callAppB(ctx, appBURL)
	if err != nil {
		span.RecordError(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"service": serviceName,
		"message": "Chamou App B com sucesso",
		"result":  result,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func callAppB(ctx context.Context, url string) (map[string]interface{}, error) {
	ctx, span := tracer.Start(ctx, "callAppB")
	defer span.End()

	span.SetAttributes(
		attribute.String("app.b.url", url),
	)

	req, err := http.NewRequestWithContext(ctx, "GET", url+"/", nil)
	if err != nil {
		return nil, err
	}

	client := http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
		Timeout:   5 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	span.SetAttributes(
		attribute.Int("http.status_code", resp.StatusCode),
	)

	return result, nil
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}
