//go:build ignore
// +build ignore

package main

import (
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/getangry/surf"
)

func main() {
	app := surf.NewApp()

	var requestCount int64

	app.Get("/", func(w http.ResponseWriter, r *http.Request) error {
		atomic.AddInt64(&requestCount, 1)
		w.Write([]byte("OK"))
		return nil
	})

	// Start counting requests per second
	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		var lastCount int64
		for {
			select {
			case <-ticker.C:
				current := atomic.LoadInt64(&requestCount)
				rps := current - lastCount
				fmt.Printf("RPS: %d, Total: %d\n", rps, current)
				lastCount = current
			}
		}
	}()

	fmt.Println("Server starting on :8080")
	fmt.Println("Test with: wrk -t12 -c400 -d30s http://localhost:8080/")

	log.Fatal(http.ListenAndServe(":8080", app))
}