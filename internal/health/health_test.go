package health

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/RakhaYandra/tora/internal/config"
)

func tcpCheck(port int) *config.Healthcheck {
	return &config.Healthcheck{Type: "tcp", Host: "127.0.0.1", Port: port,
		Interval: config.Duration(50 * time.Millisecond), Timeout: config.Duration(200 * time.Millisecond)}
}

func TestTCPWait(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if err := Wait(context.Background(), tcpCheck(port), 5*time.Second); err != nil {
		t.Fatalf("open port must pass: %v", err)
	}
}

func TestTCPTimeout(t *testing.T) {
	// Nothing listens here (TEST-NET style unroutable-ish localhost high port).
	h := tcpCheck(45999)
	start := time.Now()
	err := Wait(context.Background(), h, 300*time.Millisecond)
	if err == nil {
		t.Fatal("closed port must fail after budget")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("must stop at budget, not hang")
	}
}

func TestHTTPWait(t *testing.T) {
	mux := http.NewServeMux()
	ready := false
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		if !ready {
			w.WriteHeader(503)
			return
		}
		fmt.Fprintln(w, "ok")
	})
	srv := &http.Server{Handler: mux}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer srv.Close()
	go srv.Serve(ln)
	port := ln.Addr().(*net.TCPAddr).Port
	h := &config.Healthcheck{Type: "http", Host: "127.0.0.1", Port: port, Path: "/health",
		Interval: config.Duration(50 * time.Millisecond), Timeout: config.Duration(200 * time.Millisecond)}
	go func() {
		time.Sleep(300 * time.Millisecond)
		ready = true
	}()
	if err := Wait(context.Background(), h, 5*time.Second); err != nil {
		t.Fatalf("flapping endpoint must converge: %v", err)
	}
}

func TestHTTPNon2xxFails(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) })
	srv := &http.Server{Handler: mux}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	defer srv.Close()
	go srv.Serve(ln)
	port := ln.Addr().(*net.TCPAddr).Port
	h := &config.Healthcheck{Type: "http", Host: "127.0.0.1", Port: port, Path: "/",
		Interval: config.Duration(50 * time.Millisecond), Timeout: config.Duration(200 * time.Millisecond)}
	if err := Wait(context.Background(), h, 300*time.Millisecond); err == nil {
		t.Fatal("404 must not count as healthy")
	}
}

func TestNilCheckPasses(t *testing.T) {
	if err := Wait(context.Background(), nil, time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Wait(ctx, tcpCheck(45999), time.Minute); err == nil {
		t.Fatal("cancelled ctx must fail fast")
	}
}
