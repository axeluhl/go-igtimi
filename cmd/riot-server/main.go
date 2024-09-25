package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/igtimi/go-igtimi/riot"
	"golang.org/x/exp/slog"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))

	// example riot server implementation, receiving messages and responding to heartbeats
	server, err := riot.NewServer(ctx, riot.ServerConfig{
		Address: ":6000",
	})
	if err != nil {
		log.Fatal(err)
	}

	// example webserver for sending commands to devices
	web := &WebServer{riotServer: server}
	if err := web.Serve(ctx, ":8080"); err != nil {
		log.Fatal(err)
	}

	// example to subscribe to messages
	msgChan, unsubscribe := server.Subscribe()
	defer unsubscribe()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-msgChan:
			// Insert your own message handling here
			// e.g. store in database, send to another service, etc.
			slog.Debug("received message", "msg", fmt.Sprintf("%+v", msg))
		}
	}
}
