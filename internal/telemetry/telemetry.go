package telemetry

import (
	"context"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

type Config struct {
	Enabled     bool
	Endpoint    string
	ServiceName string
}

type Provider struct{ provider *sdktrace.TracerProvider }

// Setup installs a W3C trace-context propagator and, when configured, an OTLP
// HTTP exporter. Empty endpoint is intentionally a no-op so local development
// does not require a collector.
func Setup(ctx context.Context, config Config) (*Provider, error) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	if !config.Enabled || strings.TrimSpace(config.Endpoint) == "" {
		return &Provider{}, nil
	}
	endpoint := strings.TrimSpace(config.Endpoint)
	options := []otlptracehttp.Option{}
	if !strings.Contains(endpoint, "://") {
		options = append(options, otlptracehttp.WithInsecure())
	}
	if parsed, err := url.Parse(endpoint); err == nil && parsed.Host != "" {
		endpoint = parsed.Host
		if strings.EqualFold(parsed.Scheme, "http") {
			options = append(options, otlptracehttp.WithInsecure())
		}
	}
	options = append(options, otlptracehttp.WithEndpoint(endpoint))
	exporter, err := otlptracehttp.New(ctx, options...)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(config.ServiceName)
	if name == "" {
		name = "rag-evolution-lab"
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(name)))
	if err != nil {
		return nil, err
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter), sdktrace.WithResource(res))
	otel.SetTracerProvider(provider)
	return &Provider{provider: provider}, nil
}

func (provider *Provider) Shutdown(ctx context.Context) error {
	if provider == nil || provider.provider == nil {
		return nil
	}
	return provider.provider.Shutdown(ctx)
}

func Tracer(name string) trace.Tracer { return otel.Tracer(name) }

// ExtractRequestContext is deliberately small and HTTP-framework agnostic;
// handlers can call it before starting an application span.
func ExtractRequestContext(ctx context.Context, headers map[string]string) context.Context {
	carrier := propagation.MapCarrier{}
	for key, value := range headers {
		carrier[key] = value
	}
	return otel.GetTextMapPropagator().Extract(ctx, carrier)
}
