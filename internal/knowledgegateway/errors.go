package knowledgegateway

import (
	"fmt"
	"time"

	"go.opentelemetry.io/otel/trace"
)

type RateLimitError struct{ RetryAfter time.Duration }

func (err *RateLimitError) Error() string { return "application rate limit or quota exceeded" }

func traceParent(spanContext trace.SpanContext) string {
	if !spanContext.IsValid() {
		return ""
	}
	flags := "00"
	if spanContext.IsSampled() {
		flags = "01"
	}
	return fmt.Sprintf("00-%s-%s-%s", spanContext.TraceID().String(), spanContext.SpanID().String(), flags)
}
