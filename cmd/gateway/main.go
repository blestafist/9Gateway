package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/pestit/9gateway/internal/config"
	"github.com/pestit/9gateway/internal/httpserver"
)

func main() {
	configPath := flag.String("config", "", "path to the gateway YAML configuration")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}

	if err := http.ListenAndServe(cfg.ListenAddr, httpserver.NewHandler()); err != nil {
		log.Fatal(err)
	}
}
