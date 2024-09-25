package riot

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"golang.org/x/exp/slog"
)

const (
	DefaultRequestTimeout = 3 * time.Second
	InitialReconnectDelay = 3 * time.Second
	MaxReconnectDelay     = 24 * time.Second
)

type ClientConfig struct {
	Server    string
	Port      int
	UseTLS    bool
	Codec     CodecType
	Transport ClientTransportType

	// timeout for RIoT requests, defaults to DefaultRequestTimeout
	RequestTimeout time.Duration

	Context context.Context
}

type Client struct {
	config ClientConfig
	ctx    context.Context
	cancel context.CancelFunc
	done   sync.WaitGroup

	maxReconnectDelay time.Duration
	reconnectDelay    time.Duration
	lastConnected     time.Time
}

func NewClient(config ClientConfig) (*Client, error) {
	// config defaults
	if config.RequestTimeout == 0 {
		config.RequestTimeout = DefaultRequestTimeout
	}

	if config.Context == nil {
		config.Context = context.Background()
	}

	// set default port
	if config.Port == 0 {
		config.Port = 6000
		if config.Codec == CodecTypeJson {
			config.Port += 1
		}
		if config.UseTLS {
			config.Port += 10
		}
		if config.Transport == TransportTypeWS {
			config.Port += 1000
		}
	}

	slog.Debug("creating client", "config", config)

	ctx, cancel := context.WithCancel(config.Context)
	c := &Client{
		config:            config,
		ctx:               ctx,
		cancel:            cancel,
		maxReconnectDelay: MaxReconnectDelay,
		reconnectDelay:    InitialReconnectDelay,
	}
	return c, nil
}

type ConnectionHandler func(ctx context.Context, conn *Connection)

// Connects to the riot server and calls the handler function for each connection.
// The first connection establishment is blocking and will return an error if it fails.
// Subsequent connection failures are logged and automatically retried.
// If the handler function exits early, the connection is closed.
func (c *Client) Connect(handler ConnectionHandler) error {
	conn, err := NewConnection(c.ctx, c.config, handler)
	if err != nil {
		return err
	}
	c.done.Add(1)
	go func() {
		defer c.done.Done()
		err := conn.Wait()
		if err == nil {
			return
		}
		slog.Warn("reconnect requested", "error", err)
		c.reconnectLoop(handler)
	}()
	return nil
}

// Create a new connection to a RIoT server. Will return an error if the connection establishment fails.
func (c *Client) NewConnection(handler ConnectionHandler) (*Connection, error) {
	return NewConnection(c.ctx, c.config, handler)
}

func (c *Client) Wait() {
	c.done.Wait()
}

// ThrottleWait waits for the next connection attempt to be allowed.
func (c *Client) getReconnectDelay() time.Duration {
	if time.Now().After(c.lastConnected.Add(c.maxReconnectDelay * 10)) {
		c.reconnectDelay = InitialReconnectDelay
		return c.reconnectDelay
	}
	delay := c.reconnectDelay + time.Duration((0.5+0.5*rand.Float64())*float64(c.reconnectDelay))
	c.reconnectDelay = c.reconnectDelay * 2
	if c.reconnectDelay > c.maxReconnectDelay {
		c.reconnectDelay = c.maxReconnectDelay
	}
	return delay
}

func (c *Client) reconnectLoop(handler ConnectionHandler) {
	for {
		delay := c.getReconnectDelay()
		slog.Debug("will retry in", "delay", delay.Abs())
		select {
		case <-c.ctx.Done():
			return
		case <-time.After(delay):
		}
		c.lastConnected = time.Now()
		conn, err := NewConnection(c.ctx, c.config, handler)
		if err != nil {
			slog.Error("reconnect failed", "error", err)
			continue
		}
		err = conn.Wait()
		if err == nil {
			return
		}
		slog.Warn("reconnect requested", "error", err)
	}
}

// Close closes all connections to the RIoT server.
func (c *Client) Close() {
	c.cancel()
	c.done.Wait()
}
