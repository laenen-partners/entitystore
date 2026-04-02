package store

import (
	"context"
	"fmt"
	"time"
)

// HealthStatus is the overall health of the entity store.
type HealthStatus struct {
	DB        DBHealth         `json:"db"`
	Events    EventHealth      `json:"events"`
	Consumers []ConsumerHealth `json:"consumers,omitempty"`
}

// Healthy returns true if the database connection is OK.
func (h HealthStatus) Healthy() bool { return h.DB.OK }

// DBHealth reports database connection pool status.
type DBHealth struct {
	OK            bool          `json:"ok"`
	Latency       time.Duration `json:"latency"`
	Error         string        `json:"error,omitempty"`
	AcquiredConns int32         `json:"acquired_conns"`
	IdleConns     int32         `json:"idle_conns"`
	TotalConns    int32         `json:"total_conns"`
	MaxConns      int32         `json:"max_conns"`
}

// EventHealth reports event store activity.
type EventHealth struct {
	LastEventAt *time.Time `json:"last_event_at,omitempty"`
}

// Health returns the current health status of the store.
func (s *Store) Health(ctx context.Context) (HealthStatus, error) {
	var status HealthStatus

	// DB health: ping + pool stats.
	start := time.Now()
	pingErr := s.pool.Ping(ctx)
	status.DB.Latency = time.Since(start)
	if pingErr != nil {
		status.DB.OK = false
		status.DB.Error = pingErr.Error()
	} else {
		status.DB.OK = true
	}
	stat := s.pool.Stat()
	status.DB.AcquiredConns = stat.AcquiredConns()
	status.DB.IdleConns = stat.IdleConns()
	status.DB.TotalConns = stat.TotalConns()
	status.DB.MaxConns = stat.MaxConns()

	// Event health: last event time.
	lastTime, err := s.queries.GetLastEventTime(ctx)
	if err == nil {
		status.Events.LastEventAt = &lastTime
	}

	// Consumer health: list all consumers with lag.
	consumers, err := s.queries.ListConsumers(ctx)
	if err == nil && len(consumers) > 0 {
		status.Consumers = make([]ConsumerHealth, len(consumers))
		for i, c := range consumers {
			ch := ConsumerHealth{
				Name:        c.Name,
				LastEventAt: c.LastEventAt,
				Lag:         time.Since(c.LastEventAt).Truncate(time.Second).String(),
			}
			if c.HolderID.Valid {
				ch.HolderID = c.HolderID.String
			}
			if c.ExpiresAt.Valid {
				t := c.ExpiresAt.Time
				ch.LockExpiresAt = &t
			}
			status.Consumers[i] = ch
		}
	}

	return status, nil
}

// HealthError wraps health check into a simple ok/error for liveness probes.
func (s *Store) HealthError(ctx context.Context) error {
	start := time.Now()
	if err := s.pool.Ping(ctx); err != nil {
		return fmt.Errorf("db ping failed (%v): %w", time.Since(start), err)
	}
	return nil
}
