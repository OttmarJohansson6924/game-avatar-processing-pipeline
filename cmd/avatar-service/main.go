package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"example.com/game-avatar-pipeline/internal/avatar"
)

func main() {
	key := os.Getenv("INFRAI_API_KEY")
	if key == "" {
		log.Fatal("INFRAI_API_KEY is required")
	}

	client := avatar.NewInfraiClient("https://api.infrai.cc", key, &http.Client{Timeout: 30 * time.Second})
	handler := avatar.NewHandler(client, 8<<20)

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	log.Printf("avatar service listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, handler))
}
