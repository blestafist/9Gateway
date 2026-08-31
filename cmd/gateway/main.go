package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/pestit/9gateway/internal/config"
	"github.com/pestit/9gateway/internal/httpserver"
	"github.com/pestit/9gateway/internal/transport"
)

func main() {
	configPath := flag.String("config", "", "path to the gateway YAML configuration")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}

	upstreamClient := transport.NewClient()
	if err := http.ListenAndServe(cfg.ListenAddr, httpserver.NewHandler(upstreamClient, cfg.UpstreamBaseURL)); err != nil {
		log.Fatal(err)
	}
}
