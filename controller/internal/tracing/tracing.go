// Package tracing — OpenTelemetry 분산 트레이싱 초기화.
//
// # 사용법
//
// HCV_OTEL_ENDPOINT 환경변수가 설정되면 OTLP HTTP exporter로 트레이스를 전송한다.
// 미설정 시 no-op으로 동작하여 성능에 영향 없음.
//
//	shutdown, err := tracing.Setup("hardcorevisor-controller", endpoint)
//	defer shutdown()
package tracing

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Setup — OpenTelemetry TracerProvider를 초기화한다.
//
// endpoint가 비어 있으면 no-op shutdown을 반환하고 트레이싱을 활성화하지 않는다.
// 반환된 shutdown 함수는 defer로 호출하여 버퍼 플러시를 보장해야 한다.
func Setup(serviceName, endpoint string) (func(), error) {
	noop := func() {}

	if endpoint == "" {
		slog.Info("tracing disabled (HCV_OTEL_ENDPOINT not set)")
		return noop, nil
	}

	ctx := context.Background()

	// OTLP HTTP exporter 생성 (Jaeger, Tempo, OTEL Collector 호환)
	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(endpoint),
		otlptracehttp.WithInsecure(),
	)
	if err != nil {
		return noop, err
	}

	// 리소스 속성 (서비스 이름, 버전)
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion("0.1.0"),
		),
	)
	if err != nil {
		return noop, err
	}

	// TracerProvider 설정
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(5*time.Second),
		),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	// 글로벌 등록
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	slog.Info("tracing enabled", "endpoint", endpoint, "service", serviceName)

	// shutdown 함수: 버퍼 플러시 + provider 종료
	shutdown := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tp.Shutdown(shutdownCtx); err != nil {
			slog.Error("tracing shutdown error", "error", err)
		}
	}

	return shutdown, nil
}
