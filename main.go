package main

import (
	"context"
	"log"
)

const version = "0.1.0"

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("configuration error: %v", err)
	}

	server := newMCPServer(cfg, newImageClient(cfg))
	if err := server.Run(context.Background()); err != nil {
		log.Fatalf("MCP server stopped: %v", err)
	}
}
