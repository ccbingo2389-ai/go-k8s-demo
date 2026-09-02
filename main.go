
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

var (
	tracer trace.Tracer
	logger otellog.Logger
	meter  = otel.Meter("go-k8s-demo")
)

// 业务指标变量
var (
	requestCounter  metric.Int64Counter
	requestDuration metric.Float64Histogram
	activeRequests  metric.Int64UpDownCounter
)

func initOTel(ctx context.Context) func() {
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		endpoint = "http://localhost:4318"
	}
	svcName := os.Getenv("OTEL_SERVICE_NAME")
	if svcName == "" {
		svcName = "go-k8s-demo"
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(svcName),
			attribute.String("deployment.environment", "production"),
		),
	)
	if err != nil {
		log.Fatalf("failed to create resource: %v", err)
	}

	// ========== Traces ==========
	traceExporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(endpoint),
	)
	if err != nil {
		log.Fatalf("failed to create trace exporter: %v", err)
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	tracer = tp.Tracer("go-k8s-demo")

	// ========== Logs ==========
	logExporter, err := otlploghttp.New(ctx,
		otlploghttp.WithEndpointURL(endpoint),
	)
	if err != nil {
		log.Fatalf("failed to create log exporter: %v", err)
	}
	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(logExporter)),
		sdklog.WithResource(res),
	)
	logger = lp.Logger("go-k8s-demo")

	// ========== Metrics ==========
	metricExporter, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpointURL(endpoint),
	)
	if err != nil {
		log.Fatalf("failed to create metric exporter: %v", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(
			sdkmetric.NewPeriodicReader(metricExporter,
				sdkmetric.WithInterval(10*time.Second),
			),
		),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	// 注册业务指标
	initMetrics()

	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tp.Shutdown(ctx)
		_ = lp.Shutdown(ctx)
		_ = mp.Shutdown(ctx)
	}
}

// 初始化自定义指标
func initMetrics() {
	var err error

	requestCounter, err = meter.Int64Counter("http_requests_total",
		metric.WithDescription("Total number of HTTP requests"),
		metric.WithUnit("{request}"),
	)
	if err != nil {
		log.Fatalf("failed to create request counter: %v", err)
	}

	requestDuration, err = meter.Float64Histogram("http_request_duration_seconds",
		metric.WithDescription("HTTP request duration in seconds"),
		metric.WithUnit("s"),
	)
	if err != nil {
		log.Fatalf("failed to create request duration histogram: %v", err)
	}

	activeRequests, err = meter.Int64UpDownCounter("http_active_requests",
		metric.WithDescription("Number of active HTTP requests"),
		metric.WithUnit("{request}"),
	)
	if err != nil {
		log.Fatalf("failed to create active requests gauge: %v", err)
	}
}

func emitLog(ctx context.Context, msg string, attrs ...attribute.KeyValue) {
	fmt.Printf("%s | %s\n", time.Now().Format("2006-01-02 15:04:05.000"), msg)

	r := otellog.Record{}
	r.SetBody(otellog.StringValue(msg))
	r.SetTimestamp(time.Now())
	r.SetSeverity(otellog.SeverityInfo)
	r.SetSeverityText("INFO")
	r.AddAttributes(attrs...)
	logger.Emit(ctx, r)
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
	ctx, span := tracer.Start(r.Context(), "handle-request")
	defer span.End()

	start := time.Now()

	activeRequests.Add(ctx, 1)
	defer activeRequests.Add(ctx, -1)

	emitLog(ctx, "received request",
		attribute.String("http.method", r.Method),
		attribute.String("http.target", r.URL.Path),
		attribute.String("client.ip", r.RemoteAddr),
	)

	span.SetAttributes(
		attribute.String("http.method", r.Method),
		attribute.String("http.target", r.URL.Path),
	)

	time.Sleep(200 * time.Millisecond)

	duration := time.Since(start).Seconds()

	attrs := metric.WithAttributes(
		attribute.String("http.method", r.Method),
		attribute.String("http.target", r.URL.Path),
		attribute.Int("http.status_code", 200),
	)
	requestCounter.Add(ctx, 1, attrs)
	requestDuration.Record(ctx, duration, attrs)

	emitLog(ctx, "request handled successfully",
		attribute.String("result", "ok"),
		attribute.Float64("duration_seconds", duration),
	)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Hello from go-k8s-demo with full observability!\n"))
}

func main() {
	ctx := context.Background()
	shutdown := initOTel(ctx)
	defer shutdown()

	http.HandleFunc("/", handleRequest)

	log.Println("Starting server on :8080 ...")
	if err := http.ListenAndServe(":8080", nil); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
