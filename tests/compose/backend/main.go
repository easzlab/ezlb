package main

import (
	"encoding/json"
	"flag"
	"log"
	"net"
	"net/http"
	"sync/atomic"
)

type response struct {
	Backend string `json:"backend"`
	Source  string `json:"source"`
}

func main() {
	name := flag.String("name", "backend", "backend name returned in responses")
	flag.Parse()

	var healthy atomic.Bool
	healthy.Store(true)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response{Backend: *name, Source: host}); err != nil {
			log.Printf("encode response: %v", err)
		}
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if !healthy.Load() {
			http.Error(w, "unhealthy", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/control/down", func(w http.ResponseWriter, _ *http.Request) {
		healthy.Store(false)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/control/up", func(w http.ResponseWriter, _ *http.Request) {
		healthy.Store(true)
		w.WriteHeader(http.StatusNoContent)
	})

	log.Printf("%s listening on :8080", *name)
	log.Fatal(http.ListenAndServe(":8080", mux))
}
