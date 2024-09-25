package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/igtimi/go-igtimi/riot"
	"golang.org/x/exp/slog"
)

func main() {
	riotAddress := flag.String("riotAddress", ":6000", "Address to listen on for the riot server")
	webAddress := flag.String("webAddress", ":8080", "Address to listen on for the web interface")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})))

	// example riot server implementation, receiving messages and responding to heartbeats
	server, err := riot.NewServer(ctx, riot.ServerConfig{
		Address: *riotAddress,
	})
	if err != nil {
		log.Fatal(err)
	}

	// example webserver for sending commands to devices
	web := &WebServer{riotServer: server}
	if err := web.Serve(ctx, *webAddress); err != nil {
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
