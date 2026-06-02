package main

import (
	"artifacts-proxy/pkg/config"
	"artifacts-proxy/pkg/otel"
	"artifacts-proxy/pkg/proxy"
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	configPath := flag.String("config", "config.toml", "path to config file")
	flag.Parse()

	config, err := config.ParseFile(*configPath)
	if err != nil {
		log.Fatalf("failed to parse config: %v", err)
	}

	// Initialize OTEL if enabled
	if config.OTEL != nil && config.OTEL.Enabled {
		otelCfg := otel.Config{
			Endpoint:    config.OTEL.Endpoint,
			Insecure:    config.OTEL.Insecure,
			ServiceName: config.OTEL.ServiceName,
		}
		if err := otel.Init(otelCfg); err != nil {
			log.Printf("Warning: failed to initialize OTEL: %v", err)
		} else {
			if err := otel.InitMetrics(); err != nil {
				log.Printf("Warning: failed to initialize OTEL metrics: %v", err)
			} else {
				log.Printf("OTEL initialized: endpoint=%s, service=%s", config.OTEL.Endpoint, config.OTEL.ServiceName)
			}
		}
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", config.Port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	log.Printf("Server running on http://0.0.0.0:%d", config.Port)

	// Set up signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		log.Println("Shutting down...")
		cancel()
		
		// Give some time for graceful shutdown
		time.AfterFunc(10*time.Second, func() {
			log.Println("Forcing shutdown")
			os.Exit(1)
		})
	}()

	if err := proxy.RunServer(ctx, listener, config); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
