package riot

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/igtimi/go-igtimi/gen/go/com/igtimi"
	"nhooyr.io/websocket"
)

type ClientTransport interface {
	Read(msg *igtimi.Msg) (err error)
	Write(msg *igtimi.Msg) (err error)
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
	Addr() string
	Close() error
}

type ClientTransportType int

const (
	TransportTypeTCP ClientTransportType = iota
	TransportTypeWS
)

type ClientTransportTCP struct {
	conn   net.Conn
	codec  Codec
	reader Reader // buffed reader for codec
}

func NewClientTransportTCP(ctx context.Context, server string, port int, TLS bool, codec Codec) (*ClientTransportTCP, error) {
	var conn net.Conn
	var err error
	addr := fmt.Sprintf("%s:%d", server, port)
	dialer := &net.Dialer{
		Timeout: 15 * time.Second,
	}
	if TLS {
		tlsDialer := tls.Dialer{
			NetDialer: dialer,
			Config:    &tls.Config{ServerName: server},
		}

		conn, err = tlsDialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
	}

	return &ClientTransportTCP{conn: conn, codec: codec, reader: bufio.NewReader(conn)}, nil
}

func (t *ClientTransportTCP) Read(msg *igtimi.Msg) (err error) {
	return t.codec.Decode(t.reader, msg)
}

func (t *ClientTransportTCP) Write(msg *igtimi.Msg) (err error) {
	return t.codec.Encode(t.conn, msg)
}

func (t *ClientTransportTCP) SetReadDeadline(tim time.Time) error {
	return t.conn.SetReadDeadline(tim)
}

func (t *ClientTransportTCP) SetWriteDeadline(tim time.Time) error {
	return t.conn.SetWriteDeadline(tim)
}

func (t *ClientTransportTCP) Addr() string {
	return t.conn.RemoteAddr().String()
}

func (t *ClientTransportTCP) Close() error {
	return t.conn.Close()
}

type ClientTransportWS struct {
	conn          *websocket.Conn
	addr          string
	codec         Codec
	ctx           context.Context
	readDeadline  time.Time
	writeDeadline time.Time
	msgType       websocket.MessageType
}

func NewClientTransportWS(ctx context.Context, server string, port int, TLS bool, codec Codec) (*ClientTransportWS, error) {
	addr := fmt.Sprintf("ws://%s:%d", server, port)
	if TLS {
		addr = fmt.Sprintf("wss://%s:%d", server, port)
	}
	conn, resp, err := websocket.Dial(ctx, addr, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		buf := make([]byte, 1024)
		_, err := io.LimitReader(resp.Body, 1024).Read(buf)
		if err != nil {
			return nil, fmt.Errorf("unexpected status code %d, error while reading response: %s", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("unexpected status code %d, response: %s", resp.StatusCode, string(buf))
	}

	msgType := websocket.MessageBinary
	if _, ok := codec.(*CodecJson); ok {
		msgType = websocket.MessageText
	}

	return &ClientTransportWS{
		conn:    conn,
		addr:    addr,
		codec:   codec,
		ctx:     ctx,
		msgType: msgType,
	}, nil
}

func (t *ClientTransportWS) Read(msg *igtimi.Msg) (err error) {
	msgType, buf, err := t.conn.Read(t.ctx)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return io.EOF
		}
		return err
	}
	if msgType != t.msgType {
		return fmt.Errorf("unexpected message type %d", msgType)
	}
	return t.codec.Decode(bytes.NewBuffer(buf), msg)
}

func (t *ClientTransportWS) Write(msg *igtimi.Msg) (err error) {
	ctx := t.ctx
	cancel := func() {}
	if !t.writeDeadline.IsZero() {
		ctx, cancel = context.WithDeadline(t.ctx, t.writeDeadline)
	}
	defer cancel()
	writer, err := t.conn.Writer(ctx, t.msgType)
	if err != nil {
		return err
	}
	err = t.codec.Encode(writer, msg)
	err2 := writer.Close()
	if err != nil {
		return err
	}
	return err2
}

func (t *ClientTransportWS) SetReadDeadline(tim time.Time) error {
	t.readDeadline = tim
	return nil
}

func (t *ClientTransportWS) SetWriteDeadline(tim time.Time) error {
	t.writeDeadline = tim
	return nil
}

func (t *ClientTransportWS) Addr() string {
	return t.addr
}

func (t *ClientTransportWS) Close() error {
	return t.conn.Close(websocket.StatusNormalClosure, "")
}
