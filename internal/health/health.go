// Package health probes TCP/HTTP readiness. A check passing means the
// service accepts work; dependents wait for it (within startup budget)
// instead of merely existing as a process.
package health

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/RakhaYandra/tora/internal/config"
)

// Wait polls check until it passes, ctx ends, or budget elapses.
// TCP: connect success. HTTP: 2xx on host:port+path.
func Wait(ctx context.Context, h *config.Healthcheck, budget time.Duration) error {
	if h == nil {
		return nil
	}
	deadline := time.Now().Add(budget)
	interval := h.Interval.Std()
	if interval <= 0 {
		interval = config.DefaultHealthInterval
	}
	timeout := h.Timeout.Std()
	if timeout <= 0 {
		timeout = config.DefaultHealthTimeout
	}
	addr := fmt.Sprintf("%s:%d", h.Host, h.Port)
	for {
		if ctx.Err() != nil {
			return fmt.Errorf("healthcheck %s %s: %w", h.Type, addr, ctx.Err())
		}
		if probe(h, addr, timeout) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("healthcheck %s %s%s unhealthy after %s",
				h.Type, addr, pathSuffix(h), budget.Round(time.Second))
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("healthcheck %s %s: %w", h.Type, addr, ctx.Err())
		case <-time.After(interval):
		}
	}
}

func pathSuffix(h *config.Healthcheck) string {
	if h.Type == "http" {
		return h.Path
	}
	return ""
}

func probe(h *config.Healthcheck, addr string, timeout time.Duration) bool {
	if h.Type == "tcp" {
		c, err := net.DialTimeout("tcp", addr, timeout)
		if err != nil {
			return false
		}
		_ = c.Close()
		return true
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get("http://" + addr + h.Path)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
