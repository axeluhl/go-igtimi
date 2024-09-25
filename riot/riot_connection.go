package riot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/exp/slog"

	"github.com/igtimi/go-igtimi/gen/go/com/igtimi"
)

type Connection struct {
	config        ClientConfig
	conn          ClientTransport
	heartbeat     chan struct{}
	writeMutex    sync.Mutex
	pendingMutex  sync.Mutex
	pending       []promise
	receiverMutex sync.Mutex
	receivers     []chan *igtimi.Data
	ctx           context.Context
	cancel        context.CancelFunc
	done          sync.WaitGroup
	err           chan error
	log           *slog.Logger
}

type promise struct {
	Id        string
	Response  chan *igtimi.Msg
	ExpiresAt time.Time
}

func getID(base *igtimi.Msg) string {
	switch msg := base.Msg.(type) {
	case *igtimi.Msg_ChannelManagement:
		switch mgmt := msg.ChannelManagement.Mgmt.(type) {
		case *igtimi.ChannelManagement_Auth:
			return "Auth"
		case *igtimi.ChannelManagement_Heartbeat:
			return "Heartbeat"
		case *igtimi.ChannelManagement_Subscription:
			switch sub := mgmt.Subscription.Sub.(type) {
			case *igtimi.DataSubscription_SubscriptionRequest_:
				return fmt.Sprintf("Subscription-%s", sub.SubscriptionRequest.Id)
			case *igtimi.DataSubscription_SubscriptionResponse_:
				return fmt.Sprintf("Subscription-%s", sub.SubscriptionResponse.Id)
			case *igtimi.DataSubscription_CancelSubscription_:
				return fmt.Sprintf("CancelSubscription-%s", sub.CancelSubscription.Id)
			case *igtimi.DataSubscription_CancelResponse_:
				return fmt.Sprintf("CancelSubscription-%s", sub.CancelResponse.Id)
			}
		}
	case *igtimi.Msg_ApiData:
		return "ApiData"
	}
	return "Unknown"
}

// Create a new connection to a RIoT server. Will return an error if the connection establishment fails.
// The handler is called in a separate goroutine. If the handler exits the connection will be closed.
// The handler is called with a context and should exit when it is canceled.
func NewConnection(ctx context.Context, config ClientConfig, handler ConnectionHandler) (*Connection, error) {
	ctx, cancel := context.WithCancel(ctx)

	var codec Codec
	if config.Codec == CodecTypeProto {
		codec = &CodecProto{}
	} else if config.Codec == CodecTypeJson {
		codec = &CodecJson{}
	} else {
		cancel()
		return nil, fmt.Errorf("unknown codec type %d", config.Codec)
	}

	var conn ClientTransport
	var err error
	switch config.Transport {
	case TransportTypeTCP:
		conn, err = NewClientTransportTCP(ctx, config.Server, config.Port, config.UseTLS, codec)
		if err != nil {
			cancel()
			return nil, err
		}
	case TransportTypeWS:
		conn, err = NewClientTransportWS(ctx, config.Server, config.Port, config.UseTLS, codec)
		if err != nil {
			cancel()
			return nil, err
		}
	default:
		cancel()
		return nil, fmt.Errorf("unknown transport type %d", config.Transport)
	}

	rconn := &Connection{
		config:    config,
		conn:      conn,
		ctx:       ctx,
		cancel:    cancel,
		heartbeat: make(chan struct{}, 1),
		err:       make(chan error, 1),
		log:       slog.With("component", "riot", "address", conn.Addr()),
	}
	rconn.log.Debug("connected")
	rconn.done.Add(3)
	go rconn.handler(handler)
	go rconn.timeout()
	go rconn.reader()
	return rconn, nil
}

// Waits for connection to finish
// Returns an error if connection was closed due to an error and a reconnect is expected
func (c *Connection) Wait() error {
	c.done.Wait()
	return <-c.err
}

func (c *Connection) handler(handler ConnectionHandler) {
	defer c.done.Done()
	handler(c.ctx, c)
	c.cancel()
}

// routine timing out pending requests
func (c *Connection) timeout() {
	defer c.done.Done()
	timeout := time.NewTicker(time.Second * 1)
	heartbeatTimeout := time.NewTicker(time.Second * 30)
	defer timeout.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-timeout.C:
			c.expirePending()
		case <-c.heartbeat:
			heartbeatTimeout.Reset(time.Second * 30)
		case <-heartbeatTimeout.C:
			c.Reconnect("heartbeat timeout")
		}
	}
}

// Request a reconnect
func (c *Connection) Reconnect(reason string) {
	select {
	case c.err <- fmt.Errorf(reason):
	default:
		c.log.Error("couldn't queue error", "reason", reason)
	}
	c.cancel()
}

// Routine reading from the connection
func (c *Connection) reader() {
	defer func() {
		c.conn.Close()
		c.done.Done()

		// unblock wait
		select {
		case c.err <- nil:
		default:
		}
	}()
	for {
		// check if we should exit
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		// set deadline to avoid blocking indefinitely
		err := c.conn.SetReadDeadline(time.Now().Add(time.Second))
		if err != nil {
			c.log.Warn("set read deadline", "err", err)
		}
		var msg igtimi.Msg
		err = c.conn.Read(&msg)
		if err != nil {
			// check if we should exit
			select {
			case <-c.ctx.Done():
				return
			default:
			}

			if errors.Is(err, os.ErrDeadlineExceeded) {
				continue
			} else if err == io.EOF {
				c.Reconnect("server closed connection")
				return
			} else {
				c.Reconnect("unknown error occured")
				return
			}
		}

		if msg.Msg == nil {
			c.log.Error("empty message received")
			return
		}

		// special handling for heartbeats
		switch nested := msg.Msg.(type) {
		case *igtimi.Msg_ChannelManagement:
			switch nested.ChannelManagement.Mgmt.(type) {
			case *igtimi.ChannelManagement_Heartbeat:
				go c.handleHeartbeat()
				continue
			}
		case *igtimi.Msg_Data:
			c.receiverMutex.Lock()
			for _, receiver := range c.receivers {
				select {
				case receiver <- nested.Data:
				default:
					c.log.Warn("receiver queue full, dropping message")
				}
			}
			c.receiverMutex.Unlock()
			continue
		}

		// fulfill waiting requests
		fulfilled := c.fulfillPromise(&msg)
		if !fulfilled {
			c.log.Debug("unrequested answer", "resp", msg.String())
		}
	}
}

// Fulfill a waiting request
func (c *Connection) fulfillPromise(msg *igtimi.Msg) bool {
	id := getID(msg)
	c.pendingMutex.Lock()
	defer c.pendingMutex.Unlock()
	for index, p := range c.pending {
		if p.Id == id {
			p.Response <- msg
			c.pending = append(c.pending[:index], c.pending[index+1:]...)
			return true
		}
	}
	return false
}

// Send message to server
func (c *Connection) send(msg *igtimi.Msg) error {
	c.writeMutex.Lock()
	defer c.writeMutex.Unlock()
	err := c.conn.SetWriteDeadline(time.Now().Add(time.Second * 5))
	if err != nil {
		return err
	}
	err = c.conn.Write(msg)
	if err != nil {
		return err
	}
	c.log.Debug("send", "content", msg)
	return nil
}

// Send a message to the server and wait for the response.
// Multiple senders can be waiting for a response simultaneously.
func (c *Connection) request(msg *igtimi.Msg) (*igtimi.Msg, error) {
	pr := promise{
		Id:        getID(msg),
		Response:  make(chan *igtimi.Msg, 1),
		ExpiresAt: time.Now().Add(c.config.RequestTimeout),
	}
	c.pendingMutex.Lock()
	c.pending = append(c.pending, pr)
	c.pendingMutex.Unlock()

	err := c.send(msg)
	if err != nil {
		return nil, err
	}
	select {
	case <-c.ctx.Done():
		return nil, fmt.Errorf("connection closed")
	case resp, ok := <-pr.Response:
		if !ok {
			return nil, fmt.Errorf("request timed out")
		}
		return resp, nil
	}
}

// Expire pending requests
func (c *Connection) expirePending() {
	c.pendingMutex.Lock()
	defer c.pendingMutex.Unlock()
	now := time.Now()
	i := 0
	for _, p := range c.pending {
		if p.ExpiresAt.Before(now) {
			c.pending = append(c.pending[:i], c.pending[i+1:]...)
			close(p.Response)
			continue
		}
		i++
	}
}

// Respond to server heartbeat messages
func (c *Connection) handleHeartbeat() {
	// message timeout thread
	select {
	case c.heartbeat <- struct{}{}:
	default:
	}

	// send heartbeat response
	prev := time.Now()
	err := c.send(&igtimi.Msg{
		Msg: &igtimi.Msg_ChannelManagement{
			ChannelManagement: &igtimi.ChannelManagement{
				Mgmt: &igtimi.ChannelManagement_Heartbeat{
					Heartbeat: uint64(prev.UnixMilli()),
				},
			},
		},
	})
	if err != nil {
		c.log.Error("heartbeat", err)
		return
	}
}
