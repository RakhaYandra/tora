package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

// Minimal fixture: serves /health ok on PORT (default 18081).
// READY_DELAY (e.g. "2s") postpones listening to simulate slow boot.
func main() {
	if d := os.Getenv("READY_DELAY"); d != "" {
		if v, err := time.ParseDuration(d); err == nil {
			time.Sleep(v)
		}
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "18081"
	}
	http.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	fmt.Println("listening on :" + port)
	_ = http.ListenAndServe("127.0.0.1:"+port, nil)
}
