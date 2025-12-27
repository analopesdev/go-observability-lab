package main

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func main() {
	if err := run(); err != nil {
		log.Fatalln(err)
	}
}

func run() error {
	// Lidamos com o SIGINT (CTRL+C) de maneira segura.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Configura o OpenTelemetry.
	otelShutdown, err := setupOTelSDK(ctx)
	if err != nil {
		return err
	}
	// Lidamos com a finalização corretamente, evitando leaks.
	defer func() {
		err = errors.Join(err, otelShutdown(context.Background()))
	}()

	// Inicializamos o servidor HTTP.
	srv := &http.Server{
		Addr:         ":8080",
		BaseContext:  func(_ net.Listener) context.Context { return ctx },
		ReadTimeout:  time.Second,
		WriteTimeout: 10 * time.Second,
		Handler:      newHTTPHandler(),
	}
	srvErr := make(chan error, 1)
	go func() {
		srvErr <- srv.ListenAndServe()
	}()

	// Aguardamos por uma interrupção.
	select {
	case err = <-srvErr:
		// Erro ao inicializar o servidor HTTP.
		return err
	case <-ctx.Done():
		// Aguardamos o primeiro CTRL+C.
		// Para de receber sinais o mais rápido possível.
		stop()
	}

	// Quando o método Shutdown é chamado, ListenAndServe retornará imediatamente ErrServerClosed.
	err = srv.Shutdown(context.Background())
	return err
}

func newHTTPHandler() http.Handler {
	mux := http.NewServeMux()

	// handleFunc é uma substituição para mux.HandleFunc
	// enriquecendo ainda mais a instrumentação HTTP utilizando padrões como http.route.
	handleFunc := func(pattern string, handlerFunc func(http.ResponseWriter, *http.Request)) {
		// Configura o "http.route" para a instrumentação HTTP.
		handler := otelhttp.WithRouteTag(pattern, http.HandlerFunc(handlerFunc))
		mux.Handle(pattern, handler)
	}

	// Registra os handlers.
	handleFunc("/rolldice/", rolldice)
	handleFunc("/rolldice/{player}", rolldice)

	// Adiciona a instrumentação HTTP para todo o servidor.
	handler := otelhttp.NewHandler(mux, "/")
	return handler
}
