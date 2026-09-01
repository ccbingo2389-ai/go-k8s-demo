package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	apilog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

var (
	tracer     trace.Tracer
	otelLogger apilog.Logger
)

func main() {
	ctx := context.Background()

	// ---------- 1. Resource ----------
	svcName := os.Getenv("OTEL_SERVICE_NAME")
	if svcName == "" {
		svcName = "go-k8s-demo"
	}
	res, err := sdkresource.New(ctx,
		sdkresource.WithAttributes(semconv.ServiceName(svcName)),
	)
	if err != nil {
		log.Fatalf("failed to create resource: %v", err)
	}

	// ---------- 2. OTLP endpoint ----------
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://signoz-otel-collector.signoz.svc.cluster.local:4318"
	}

	// ---------- 3. Traces ----------
	tp, err := initTracer(ctx, endpoint, res)
	if err != nil {
		log.Fatalf("failed to init tracer: %v", err)
	}
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	tracer = tp.Tracer("go-k8s-demo")

	// ---------- 4. Logs ----------
	lp, err := initLogger(ctx, endpoint, res)
	if err != nil {
		log.Fatalf("failed to init logger: %v", err)
	}
	otelLogger = lp.Logger("go-k8s-demo")

	// ---------- 5. HTTP 服务 ----------
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRequest)
	srv := &http.Server{Addr: ":8080", Handler: mux}

	go func() {
		log.Println("Starting server on :8080 ...")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// ---------- 6. 优雅退出 ----------
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down ...")

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	_ = lp.Shutdown(shutCtx)
	_ = tp.Shutdown(shutCtx)
	log.Println("Bye.")
}

func initTracer(ctx context.Context, endpoint string, res *sdkresource.Resource) (*sdktrace.TracerProvider, error) {
	exp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint+"/v1/traces"))
	if err != nil {
		return nil, err
	}
	return sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
	), nil
}

func initLogger(ctx context.Context, endpoint string, res *sdkresource.Resource) (*sdklog.LoggerProvider, error) {
	exp, err := otlploghttp.New(ctx, otlploghttp.WithEndpointURL(endpoint+"/v1/logs"))
	if err != nil {
		return nil, err
	}
	return sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)),
		sdklog.WithResource(res),
	), nil
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// 根 span
	ctx, rootSpan := tracer.Start(ctx, "GET /",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithAttributes(
			attribute.String("http.request.method", r.Method),
			attribute.String("url.path", r.URL.Path),
			attribute.String("client.address", r.RemoteAddr),
		),
	)
	defer rootSpan.End()

	// ✅ 日志①：全部改用 attribute.String / attribute.StringValue
	emitLog(ctx, apilog.SeverityInfo, "received request",
		attribute.String("http.method", r.Method),
		attribute.String("http.target", r.URL.Path),
		attribute.String("client.ip", r.RemoteAddr),
	)

	// 子 span
	ctx, childSpan := tracer.Start(ctx, "handle-request")
	defer childSpan.End()

	time.Sleep(200 * time.Millisecond)

	// ✅ 日志②
	emitLog(ctx, apilog.SeverityInfo, "request handled successfully",
		attribute.String("result", "ok"),
	)

	rootSpan.SetAttributes(attribute.Int("http.response.status_code", http.StatusOK))
	fmt.Fprintln(w, "Hello from go-k8s-demo! Tracing + Logging active.")
}

// emitLog 适配 v0.22.0：
//   - Record.SetBody 接受 attribute.Value
//   - Record.AddAttributes 接受 ...attribute.KeyValue
func emitLog(ctx context.Context, sev apilog.Severity, body string, attrs ...attribute.KeyValue) {
	var rec apilog.Record
	rec.SetBody(attribute.StringValue(body)) // ✅ attribute.StringValue，不是 apilog.StringValue
	rec.SetSeverity(sev)
	rec.SetTimestamp(time.Now())
	rec.AddAttributes(attrs...) // ✅ attribute.KeyValue，不是 apilog.KeyValue
	otelLogger.Emit(ctx, rec)
}
