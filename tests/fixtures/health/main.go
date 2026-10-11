package main

import (
	"fmt"
	"net/http"
	"os"
)

// Minimal fixture: serves /health ok on PORT (default 18081).
func main() {
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
