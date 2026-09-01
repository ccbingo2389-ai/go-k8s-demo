package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	apilog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

var logger apilog.Logger

func main() {
	ctx := context.Background()

	// 统一的 resource（service.name 等）
	res, err := sdkresource.New(ctx,
		sdkresource.WithAttributes(
			semconv.ServiceName(os.Getenv("OTEL_SERVICE_NAME")),
		),
	)
	if err != nil {
		log.Fatalf("failed to create resource: %v", err)
	}

	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") // http://signoz-otel-collector.signoz.svc.cluster.local:4318

	// ===== Trace（你已有的部分）=====
	tp, err := initTracer(ctx, endpoint, res)
	if err != nil {
		log.Fatalf("failed to init tracer: %v", err)
	}
	defer func() { _ = tp.Shutdown(ctx) }()

	// ===== Logs（新增）=====
	lp, err := initLogger(ctx, endpoint, res)
	if err != nil {
		log.Fatalf("failed to init logger: %v", err)
	}
	defer func() { _ = lp.Shutdown(ctx) }()
	logger = lp.Logger("go-k8s-demo")

	http.HandleFunc("/", handleRequest)
	log.Println("Starting server on :8080...")
	log.Fatal(http.ListenAndServe(":8080", nil))
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
	spanCtx := trace.SpanContextFromContext(ctx)

	// ===== 发一条结构化日志，trace_id/span_id 由 SDK 自动关联 =====
	var rec apilog.Record
	rec.SetBody(apilog.StringValue("handling incoming request"))
	rec.SetSeverity(apilog.SeverityInfo)
	rec.SetTimestamp(time.Now())
	rec.AddAttributes(
		apilog.String("http.method", r.Method),
		apilog.String("http.target", r.URL.Path),
		apilog.String("client.ip", r.RemoteAddr),
		apilog.String("trace_id", spanCtx.TraceID().String()), // 显式带一份，方便检索
	)
	logger.Emit(ctx, rec)

	// 模拟业务耗时
	time.Sleep(200 * time.Millisecond)

	fmt.Fprintln(w, "Hello from go-k8s-demo! Tracing + Logging active.")
}
