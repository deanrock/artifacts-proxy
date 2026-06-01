package main

import (
	"artifacts-proxy/pkg/config"
	"artifacts-proxy/pkg/proxy"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
)

func main() {
	configPath := flag.String("config", "config.toml", "path to config file")
	flag.Parse()

	logLevel, ok := os.LookupEnv("LOG_LEVEL")
	if ok {
		switch logLevel {
		case "debug":
			slog.SetLogLoggerLevel(slog.LevelDebug)
		case "info":
			slog.SetLogLoggerLevel(slog.LevelInfo)
		case "warn":
			slog.SetLogLoggerLevel(slog.LevelWarn)
		case "error":
			slog.SetLogLoggerLevel(slog.LevelError)
		default:
			log.Fatalf("Invalid LOG_LEVEL specified: '%s'. Supported: debug/info/warn/error", logLevel)
		}
	}

	config, err := config.ParseFile(*configPath)
	if err != nil {
		log.Fatalf("failed to parse config: %v", err)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", config.Port))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	log.Printf("Server running on http://0.0.0.0:%d", config.Port)

	if err := proxy.RunServer(listener, config); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
