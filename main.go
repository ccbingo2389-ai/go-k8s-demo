package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
)

func initTracer() func(context.Context) error {
	ctx := context.Background()

	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://localhost:4318"
	}
	serviceName := os.Getenv("OTEL_SERVICE_NAME")
	if serviceName == "" {
		serviceName = "go-k8s-demo"
	}

	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(endpoint+"/v1/traces"),
	)
	if err != nil {
		log.Fatalf("failed to create exporter: %v", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
		),
	)
	if err != nil {
		log.Fatalf("failed to create resource: %v", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	return tp.Shutdown
}

func main() {
	shutdown := initTracer()

	tracer := otel.Tracer("go-k8s-demo-server")

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		ctx, span := tracer.Start(r.Context(), "handle-request")
		defer span.End()

		w.Write([]byte("Hello from go-k8s-demo! Tracing is active.\n"))

		_, childSpan := tracer.Start(ctx, "simulate-work")
		defer childSpan.End()
	})

	handler := otelhttp.NewHandler(http.DefaultServeMux, "server")

	srv := &http.Server{
		Addr:    ":8080",
		Handler: handler,
	}

	// ✅ 优雅关闭：确保 buffered spans 被 flush
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// 先停 HTTP，再 flush traces
		srv.Shutdown(shutdownCtx)
		if err := shutdown(shutdownCtx); err != nil {
			log.Printf("OTel shutdown error: %v", err)
		}
	}()

	log.Println("Starting server on :8080...")
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
