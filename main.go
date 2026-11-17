package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/xorpaul/go-icinga-mcp/internal/config"
	"github.com/xorpaul/go-icinga-mcp/internal/icinga"
	"github.com/xorpaul/go-icinga-mcp/internal/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "go-icinga-mcp: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to YAML config file (default: <binary-dir>/config.yaml)")
	transport := flag.String("transport", "stdio", "MCP transport: stdio | streamable-http | sse")
	port := flag.Int("port", 8080, "TCP port to listen on (streamable-http and sse only)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	client, err := icinga.NewClient(cfg)
	if err != nil {
		return err
	}
	svc := icinga.NewService(client, cfg)
	srv := mcpserver.New(cfg, svc)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.SetOutput(os.Stderr)

	switch *transport {
	case "stdio":
		if err := srv.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
			return err
		}
		return nil
	case "streamable-http":
		h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil)
		return serveHTTP(ctx, h, *port)
	case "sse":
		h := mcp.NewSSEHandler(func(*http.Request) *mcp.Server { return srv }, nil)
		return serveHTTP(ctx, h, *port)
	default:
		return fmt.Errorf("unknown transport %q: use stdio, streamable-http, or sse", *transport)
	}
}

func serveHTTP(ctx context.Context, h http.Handler, port int) error {
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	log.Printf("listening on %s", addr)

	srv := &http.Server{Handler: h}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case <-ctx.Done():
		return srv.Shutdown(context.Background())
	case err := <-errCh:
		return err
	}
}
