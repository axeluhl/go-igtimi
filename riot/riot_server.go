package riot

import (
	"bufio"
	"context"
	"fmt"
	"maps"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/igtimi/go-igtimi/gen/go/com/igtimi"
	"golang.org/x/exp/slog"
)

type ServerConfig struct {
	Address string
	Codec   CodecType
}

type Server struct {
	config         ServerConfig
	listener       net.Listener
	connections    []*ServerConnection
	connMutex      sync.Mutex
	subscriptions  []chan *igtimi.Msg
	subscribeMutex sync.RWMutex
}

func NewServer(ctx context.Context, config ServerConfig) (*Server, error) {
	listener, err := net.Listen("tcp", config.Address)
	if err != nil {
		return nil, err
	}
	slog.Debug("listening", "address", config.Address)
	s := &Server{
		config:   config,
		listener: listener,
	}
	go s.accept(ctx)
	return s, nil
}

func (s *Server) accept(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		conn, err := s.listener.Accept()
		if err != nil {
			slog.Error("accept failed", "err", err)
			return
		}
		slog.Debug("client connected", "address", conn.RemoteAddr())
		c, err := newServerConnection(ctx, conn, s)
		if err != nil {
			slog.Error("newServerConnection failed", "err", err)
			continue
		}
		s.addConnection(c)
	}
}

func (s *Server) forward(msg *igtimi.Msg) {
	s.subscribeMutex.RLock()
	defer s.subscribeMutex.RUnlock()
	for _, sub := range s.subscriptions {
		select {
		case sub <- msg:
		default:
			slog.Warn("subscription buffer full, dropping message")
		}
	}
}

func (s *Server) addConnection(c *ServerConnection) {
	s.connMutex.Lock()
	defer s.connMutex.Unlock()
	s.connections = append(s.connections, c)
}

func (s *Server) removeConnection(c *ServerConnection) {
	s.connMutex.Lock()
	defer s.connMutex.Unlock()
	for i, conn := range s.connections {
		if conn == c {
			s.connections = slices.Delete(s.connections, i, i+1)
			return
		}
	}
}

type UnsubscribeFunc func()

// Subscribe returns a channel that receives messages from all connected clients
// and an unsubscribe function, which should be called when the subscription is no longer needed
func (s *Server) Subscribe() (<-chan *igtimi.Msg, UnsubscribeFunc) {
	ch := make(chan *igtimi.Msg, 128)
	s.subscribeMutex.Lock()
	defer s.subscribeMutex.Unlock()
	s.subscriptions = append(s.subscriptions, ch)
	return ch, func() {
		s.subscribeMutex.Lock()
		defer s.subscribeMutex.Unlock()
		for i, sub := range s.subscriptions {
			if sub == ch {
				s.subscriptions = slices.Delete(s.subscriptions, i, i+1)
				close(ch)
				return
			}
		}
	}
}

// GetConnections returns a list of all active connections
func (s *Server) GetConnections() []*ServerConnection {
	s.connMutex.Lock()
	defer s.connMutex.Unlock()
	return slices.Clone(s.connections)
}

func (s *Server) Send(serial string, msg *igtimi.Msg) error {
	s.connMutex.Lock()
	defer s.connMutex.Unlock()
	for _, conn := range s.connections {
		if conn.GetSerial() != nil && *conn.GetSerial() == serial {
			return conn.Send(msg)
		}
	}
	return fmt.Errorf("device not connected")
}

// ServerConnection represents a connection to a client
type ServerConnection struct {
	log           *slog.Logger
	server        *Server
	conn          net.Conn
	config        ServerConfig
	codec         Codec
	heartbeatRecv chan struct{}
	errChan       chan error

	serial *string
	info   map[string]string
	mutex  sync.Mutex
}

func newServerConnection(ctx context.Context, conn net.Conn, server *Server) (*ServerConnection, error) {
	config := server.config
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
	s := &ServerConnection{
		log:           slog.With("address", conn.RemoteAddr()),
		conn:          conn,
		config:        config,
		codec:         codec,
		server:        server,
		heartbeatRecv: make(chan struct{}, 1),
		errChan:       make(chan error, 1),
		info:          make(map[string]string),
	}
	go s.read(ctx)
	go s.heartbeat(ctx)
	go func() {
		defer server.removeConnection(s)
		defer s.conn.Close()
		defer cancel()
		select {
		case <-ctx.Done():
			return
		case err := <-s.errChan:
			s.log.Error("connection error", "err", err)
			return
		}
	}()
	return s, nil
}

func (s *ServerConnection) error(err error) {
	select {
	case s.errChan <- err:
	default:
	}
}

func (s *ServerConnection) read(ctx context.Context) {
	rd := bufio.NewReader(s.conn)
	for {
		var msg igtimi.Msg
		select {
		case <-ctx.Done():
			return
		default:
		}
		err := s.codec.Decode(rd, &msg)
		if err != nil {
			s.error(fmt.Errorf("decode failed: %w", err))
			return
		}

		// handle any incoming msg as heartbeat
		select {
		case s.heartbeatRecv <- struct{}{}:
		default:
		}

		// handle serial number in data messages
		if s.serial == nil && msg.GetData() != nil {
			for _, data := range msg.GetData().Data {
				if data.GetSerialNumber() != "" {
					s.mutex.Lock()
					serial := data.GetSerialNumber()
					s.serial = &serial
					s.log.Debug("serial number received", "serial", serial)
					s.mutex.Unlock()
					break
				}
			}
		}
		// store device information
		if msg.GetDeviceManagement() != nil && msg.GetDeviceManagement().GetUpdateDeviceInformation() != nil {
			s.mutex.Lock()
			s.info = msg.GetDeviceManagement().GetUpdateDeviceInformation().Info
			s.mutex.Unlock()
		}

		s.server.forward(&msg)
	}
}

// heartbeat sends and receives heartbeats to/from the client
// clients expect a heartbeat every 15 seconds
func (s *ServerConnection) heartbeat(ctx context.Context) {
	ticker := time.NewTicker(15 * time.Second)
	lastHeartbeat := time.Now()
	if !s.sendHeartbeat() {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.heartbeatRecv:
			lastHeartbeat = time.Now()
			continue
		case <-ticker.C:
		}
		// time out a client if no heartbeat is received
		if time.Since(lastHeartbeat) > 30*time.Second {
			s.error(fmt.Errorf("heartbeat timeout"))
			return
		}
		// send outgoing heartbeat
		if !s.sendHeartbeat() {
			return
		}
	}
}

func (s *ServerConnection) sendHeartbeat() bool {
	// s.log.Debug("sending heartbeat")
	err := s.codec.Encode(s.conn, &igtimi.Msg{
		Msg: &igtimi.Msg_ChannelManagement{
			ChannelManagement: &igtimi.ChannelManagement{
				Mgmt: &igtimi.ChannelManagement_Heartbeat{
					Heartbeat: uint64(time.Now().Unix()),
				},
			},
		},
	})
	if err != nil {
		s.error(fmt.Errorf("failed to send heartbeat: %w", err))
		return false
	}
	return true
}

func (s *ServerConnection) GetSerial() *string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.serial
}

func (s *ServerConnection) GetInfo() map[string]string {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return maps.Clone(s.info)
}

func (s *ServerConnection) Send(msg *igtimi.Msg) error {
	s.log.Debug("send", "content", msg)
	err := s.codec.Encode(s.conn, msg)
	if err != nil {
		return fmt.Errorf("send failed: %w", err)
	}
	return nil
}
