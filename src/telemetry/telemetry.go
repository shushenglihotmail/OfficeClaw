// Package telemetry provides OpenTelemetry tracing and Prometheus metrics.
package telemetry

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelprometheus "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/officeclaw/src/config"
)

// Provider wraps the OpenTelemetry providers for shutdown.
type Provider struct {
	tracerProvider *sdktrace.TracerProvider
	meterProvider  *sdkmetric.MeterProvider
	httpServer     *http.Server
}

// Metrics holds all OfficeClaw Prometheus counters/histograms.
type Metrics struct {
	MessagesReceived  metric.Int64Counter
	MessagesProcessed metric.Int64Counter
	MessageErrors     metric.Int64Counter
	LLMRequests       metric.Int64Counter
	LLMLatency        metric.Float64Histogram
	LLMTokensIn       metric.Int64Counter
	LLMTokensOut      metric.Int64Counter
	ToolCalls         metric.Int64Counter
	ToolErrors        metric.Int64Counter
	ToolLatency       metric.Float64Histogram
	TasksExecuted     metric.Int64Counter
	TaskErrors        metric.Int64Counter
	TaskLatency       metric.Float64Histogram
	AuthRefreshes     metric.Int64Counter
	AuthErrors        metric.Int64Counter
}

// GlobalMetrics is the singleton metrics instance.
var GlobalMetrics *Metrics

// Init initializes OpenTelemetry tracing and Prometheus metrics.
func Init(cfg config.TelemetryConfig) (*Provider, error) {
	if !cfg.Enabled {
		return &Provider{}, nil
	}

	provider := &Provider{}

	// Create resource with service info
	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String(cfg.OTel.ServiceName),
			semconv.ServiceVersionKey.String("1.0.0"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("creating resource: %w", err)
	}

	// Setup tracer provider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	provider.tracerProvider = tp

	// Setup Prometheus meter provider
	if cfg.Prometheus.Enabled {
		promExporter, err := otelprometheus.New()
		if err != nil {
			return nil, fmt.Errorf("creating prometheus exporter: %w", err)
		}

		mp := sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(res),
			sdkmetric.WithReader(promExporter),
		)
		otel.SetMeterProvider(mp)
		provider.meterProvider = mp

		// Register metrics
		if err := registerMetrics(mp); err != nil {
			return nil, fmt.Errorf("registering metrics: %w", err)
		}

		// Start Prometheus HTTP server
		mux := http.NewServeMux()
		mux.Handle(cfg.Prometheus.Path, promhttp.HandlerFor(
			prometheus.DefaultGatherer,
			promhttp.HandlerOpts{EnableOpenMetrics: true},
		))

		addr := fmt.Sprintf(":%d", cfg.Prometheus.Port)
		provider.httpServer = &http.Server{Addr: addr, Handler: mux}

		go func() {
			log.Printf("[telemetry] Prometheus metrics at http://localhost%s%s", addr, cfg.Prometheus.Path)
			if err := provider.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("[telemetry] Prometheus server error: %v", err)
			}
		}()
	}

	return provider, nil
}

// Shutdown cleanly shuts down telemetry providers.
func (p *Provider) Shutdown(ctx context.Context) {
	if p.httpServer != nil {
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		p.httpServer.Shutdown(shutdownCtx)
	}
	if p.tracerProvider != nil {
		p.tracerProvider.Shutdown(ctx)
	}
	if p.meterProvider != nil {
		p.meterProvider.Shutdown(ctx)
	}
}

// Tracer returns a named tracer.
func Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}

// registerMetrics creates and registers all OfficeClaw metrics.
func registerMetrics(mp *sdkmetric.MeterProvider) error {
	meter := mp.Meter("officeclaw")

	m := &Metrics{}
	var err error

	m.MessagesReceived, err = meter.Int64Counter("officeclaw.messages.received",
		metric.WithDescription("Total messages received from email/Teams"))
	if err != nil {
		return err
	}

	m.MessagesProcessed, err = meter.Int64Counter("officeclaw.messages.processed",
		metric.WithDescription("Total messages successfully processed"))
	if err != nil {
		return err
	}

	m.MessageErrors, err = meter.Int64Counter("officeclaw.messages.errors",
		metric.WithDescription("Total message processing errors"))
	if err != nil {
		return err
	}

	m.LLMRequests, err = meter.Int64Counter("officeclaw.llm.requests",
		metric.WithDescription("Total LLM API requests"))
	if err != nil {
		return err
	}

	m.LLMLatency, err = meter.Float64Histogram("officeclaw.llm.latency_seconds",
		metric.WithDescription("LLM request latency in seconds"))
	if err != nil {
		return err
	}

	m.LLMTokensIn, err = meter.Int64Counter("officeclaw.llm.tokens_in",
		metric.WithDescription("Total input tokens sent to LLM"))
	if err != nil {
		return err
	}

	m.LLMTokensOut, err = meter.Int64Counter("officeclaw.llm.tokens_out",
		metric.WithDescription("Total output tokens from LLM"))
	if err != nil {
		return err
	}

	m.ToolCalls, err = meter.Int64Counter("officeclaw.tools.calls",
		metric.WithDescription("Total tool invocations"))
	if err != nil {
		return err
	}

	m.ToolErrors, err = meter.Int64Counter("officeclaw.tools.errors",
		metric.WithDescription("Total tool errors"))
	if err != nil {
		return err
	}

	m.ToolLatency, err = meter.Float64Histogram("officeclaw.tools.latency_seconds",
		metric.WithDescription("Tool execution latency in seconds"))
	if err != nil {
		return err
	}

	m.TasksExecuted, err = meter.Int64Counter("officeclaw.tasks.executed",
		metric.WithDescription("Total tasks executed"))
	if err != nil {
		return err
	}

	m.TaskErrors, err = meter.Int64Counter("officeclaw.tasks.errors",
		metric.WithDescription("Total task errors"))
	if err != nil {
		return err
	}

	m.TaskLatency, err = meter.Float64Histogram("officeclaw.tasks.latency_seconds",
		metric.WithDescription("Task execution latency in seconds"))
	if err != nil {
		return err
	}

	m.AuthRefreshes, err = meter.Int64Counter("officeclaw.auth.refreshes",
		metric.WithDescription("Total auth token refreshes"))
	if err != nil {
		return err
	}

	m.AuthErrors, err = meter.Int64Counter("officeclaw.auth.errors",
		metric.WithDescription("Total auth errors"))
	if err != nil {
		return err
	}

	GlobalMetrics = m
	return nil
}

// RecordLLMRequest records metrics for an LLM API call.
func RecordLLMRequest(ctx context.Context, provider, model, status string, duration float64, tokensIn, tokensOut int64) {
	if GlobalMetrics == nil {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("provider", provider),
		attribute.String("model", model),
		attribute.String("status", status),
	}
	GlobalMetrics.LLMRequests.Add(ctx, 1, metric.WithAttributes(attrs...))
	GlobalMetrics.LLMLatency.Record(ctx, duration, metric.WithAttributes(attrs...))
	if tokensIn > 0 {
		GlobalMetrics.LLMTokensIn.Add(ctx, tokensIn, metric.WithAttributes(attrs...))
	}
	if tokensOut > 0 {
		GlobalMetrics.LLMTokensOut.Add(ctx, tokensOut, metric.WithAttributes(attrs...))
	}
}

// RecordToolCall records metrics for a tool invocation.
func RecordToolCall(ctx context.Context, toolName, status string, duration float64) {
	if GlobalMetrics == nil {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("tool", toolName),
		attribute.String("status", status),
	}
	GlobalMetrics.ToolCalls.Add(ctx, 1, metric.WithAttributes(attrs...))
	GlobalMetrics.ToolLatency.Record(ctx, duration, metric.WithAttributes(attrs...))
	if status == "error" {
		GlobalMetrics.ToolErrors.Add(ctx, 1, metric.WithAttributes(attrs...))
	}
}

// RecordTaskExecution records metrics for a task execution.
func RecordTaskExecution(ctx context.Context, taskName, status string, duration float64) {
	if GlobalMetrics == nil {
		return
	}
	attrs := []attribute.KeyValue{
		attribute.String("task", taskName),
		attribute.String("status", status),
	}
	GlobalMetrics.TasksExecuted.Add(ctx, 1, metric.WithAttributes(attrs...))
	GlobalMetrics.TaskLatency.Record(ctx, duration, metric.WithAttributes(attrs...))
	if status == "error" || status == "timeout" {
		GlobalMetrics.TaskErrors.Add(ctx, 1, metric.WithAttributes(attrs...))
	}
}
