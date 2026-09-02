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
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp" // 🆕 metrics exporter
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	apilog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric" // 🆕 metrics API
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric" // 🆕 metrics SDK
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

var (
	tracer     trace.Tracer
	otelLogger apilog.Logger
	meter      = otel.Meter("go-k8s-demo") // 🆕 全局 meter
)

// 🆕 业务指标变量
var (
	requestCounter  metric.Int64Counter
	requestDuration metric.Float64Histogram
	activeRequests  metric.Int64UpDownCounter
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

	// ---------- 🆕 5. Metrics ----------
	mp, err := initMetrics(ctx, endpoint, res)
	if err != nil {
		log.Fatalf("failed to init metrics: %v", err)
	}
	otel.SetMeterProvider(mp)
	initBusinessMetrics() // 🆕 注册自定义指标

	// ---------- 6. HTTP 服务 ----------
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleRequest)
	srv := &http.Server{Addr: ":8080", Handler: mux}

	go func() {
		log.Println("Starting server on :8080 ...")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// ---------- 7. 优雅退出 ----------
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("Shutting down ...")

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	_ = mp.Shutdown(shutCtx) // 🆕 关闭 metrics provider
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

// 🆕 初始化 Metrics Provider
func initMetrics(ctx context.Context, endpoint string, res *sdkresource.Resource) (*sdkmetric.MeterProvider, error) {
	exp, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(endpoint+"/v1/metrics"))
	if err != nil {
		return nil, err
	}
	return sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(
			sdkmetric.NewPeriodicReader(exp,
				sdkmetric.WithInterval(10*time.Second),
			),
		),
		sdkmetric.WithResource(res),
	), nil
}

// 🆕 注册自定义业务指标
func initBusinessMetrics() {
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
		log.Fatalf("failed to create active requests counter: %v", err)
	}
}

func handleRequest(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	start := time.Now() // 🆕 记录开始时间

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

	// 🆕 活跃请求 +1
	activeRequests.Add(ctx, 1)
	defer activeRequests.Add(ctx, -1)

	emitLog(ctx, apilog.SeverityInfo, "received request",
		attribute.String("http.method", r.Method),
		attribute.String("http.target", r.URL.Path),
		attribute.String("client.ip", r.RemoteAddr),
	)

	// 子 span
	ctx, childSpan := tracer.Start(ctx, "handle-request")
	defer childSpan.End()

	time.Sleep(200 * time.Millisecond)

	duration := time.Since(start).Seconds() // 🆕 计算耗时

	// 🆕 记录指标
	metricAttrs := metric.WithAttributes(
		attribute.String("http.method", r.Method),
		attribute.String("http.target", r.URL.Path),
		attribute.Int("http.status_code", http.StatusOK),
	)
	requestCounter.Add(ctx, 1, metricAttrs)
	requestDuration.Record(ctx, duration, metricAttrs)

	emitLog(ctx, apilog.SeverityInfo, "request handled successfully",
		attribute.String("result", "ok"),
		attribute.Float64("duration_seconds", duration), // 🆕 日志也带耗时
	)

	rootSpan.SetAttributes(attribute.Int("http.response.status_code", http.StatusOK))
	fmt.Fprintln(w, "Hello from go-k8s-demo! Tracing + Logging + Metrics active.")
}

func emitLog(ctx context.Context, sev apilog.Severity, body string, attrs ...attribute.KeyValue) {
	var rec apilog.Record
	rec.SetBody(attribute.StringValue(body))
	rec.SetSeverity(sev)
	rec.SetTimestamp(time.Now())
	rec.AddAttributes(attrs...)
	otelLogger.Emit(ctx, rec)
}
