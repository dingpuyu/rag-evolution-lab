package trace

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/dingpuyu/rag-evolution-lab/internal/domain"
)

var sequence atomic.Uint64

type Recorder struct {
	trace   domain.QueryTrace
	started time.Time
}

func New(pipeline, query string) *Recorder {
	started := time.Now()
	return &Recorder{
		trace: domain.QueryTrace{
			ID:       fmt.Sprintf("trace_%d_%d", started.UnixNano(), sequence.Add(1)),
			Pipeline: pipeline,
			Query:    query,
		},
		started: started,
	}
}

func (r *Recorder) Add(name string, started time.Time, attributes map[string]any) {
	r.trace.Events = append(r.trace.Events, domain.TraceEvent{
		Name:       name,
		StartedAt:  started,
		DurationMS: time.Since(started).Milliseconds(),
		Attributes: attributes,
	})
}

func (r *Recorder) Finish() domain.QueryTrace {
	r.trace.TotalMS = time.Since(r.started).Milliseconds()
	return r.trace
}
