package auth

import (
	"sync"
	"time"
)

type AuditEvent struct {
	RequestID  string    `json:"request_id"`
	Timestamp  time.Time `json:"timestamp"`
	Subject    string    `json:"subject,omitempty"`
	TenantID   string    `json:"tenant_id,omitempty"`
	Roles      []string  `json:"roles,omitempty"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Decision   string    `json:"decision"`
	Reason     string    `json:"reason,omitempty"`
	Status     int       `json:"status"`
	DurationMS float64   `json:"duration_ms"`
}

type AuditLog struct {
	mu     sync.RWMutex
	limit  int
	events []AuditEvent
}

func NewAuditLog(limit int) *AuditLog {
	if limit <= 0 {
		limit = 200
	}
	return &AuditLog{limit: limit}
}

func (log *AuditLog) Append(event AuditEvent) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.events = append(log.events, event)
	if len(log.events) > log.limit {
		log.events = append([]AuditEvent(nil), log.events[len(log.events)-log.limit:]...)
	}
}

func (log *AuditLog) Recent(limit int) []AuditEvent {
	log.mu.RLock()
	defer log.mu.RUnlock()
	if limit <= 0 || limit > len(log.events) {
		limit = len(log.events)
	}
	result := make([]AuditEvent, 0, limit)
	for index := len(log.events) - 1; index >= len(log.events)-limit; index-- {
		result = append(result, log.events[index])
	}
	return result
}
