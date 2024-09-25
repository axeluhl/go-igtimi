package main

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"log"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/igtimi/go-igtimi/gen/go/com/igtimi"
	"github.com/igtimi/go-igtimi/riot"
)

//go:embed assets
var assets embed.FS

//go:embed template
var templateFS embed.FS

var templates = template.Must(template.New("").
	Funcs(template.FuncMap{"hasField": hasField}).
	ParseFS(templateFS, "template/*.html"))

func hasField(v interface{}, name string) bool {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return false
	}
	return rv.FieldByName(name).IsValid()
}

type WebServer struct {
	riotServer *riot.Server
}

func (ws *WebServer) Serve(ctx context.Context, address string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /index.html", ws.handleIndex)
	mux.HandleFunc("GET /devices", ws.handleDevices)
	mux.HandleFunc("GET /device", ws.handleDevice)
	mux.HandleFunc("GET /logs", ws.handleLogs)
	mux.HandleFunc("POST /command", ws.handleCommand)
	mux.Handle("GET /assets/", http.FileServer(http.FS(assets)))

	s := http.Server{
		Handler:     mux,
		ReadTimeout: 30 * time.Second,
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	go func() {
		if err := s.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}()
	go func() {
		<-ctx.Done()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			slog.Error("shutdown failed", "err", err)
		}
	}()
	return nil
}

func (ws *WebServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	serial := r.URL.Query().Get("serial")
	devices := ws.getDevices()
	data := struct {
		Serial   string
		Devices  []Device
		IsOnline bool
	}{
		Serial:  serial,
		Devices: devices,
		IsOnline: slices.ContainsFunc(devices, func(dev Device) bool {
			return dev.Serial == serial
		}),
	}
	if err := templates.ExecuteTemplate(w, "index", data); err != nil {
		slog.Error("template execution failed", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type Device struct {
	Serial string
}

func (ws *WebServer) getDevices() []Device {
	devices := []Device{}
	for _, conn := range ws.riotServer.GetConnections() {
		if conn.GetSerial() == nil {
			continue
		}
		devices = append(devices, Device{
			Serial: *conn.GetSerial(),
		})
	}
	return devices
}

func (ws *WebServer) handleDevice(w http.ResponseWriter, r *http.Request) {
	serial := r.URL.Query().Get("serial")
	if serial == "" {
		http.Error(w, "serial required", http.StatusBadRequest)
		return
	}
	devices := ws.getDevices()
	data := struct {
		Serial   string
		IsOnline bool
	}{
		Serial: serial,
		IsOnline: slices.ContainsFunc(devices, func(dev Device) bool {
			return dev.Serial == serial
		}),
	}
	if err := templates.ExecuteTemplate(w, "device", data); err != nil {
		slog.Error("template execution failed", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (ws *WebServer) handleDevices(w http.ResponseWriter, r *http.Request) {
	data := struct {
		Serial  string
		Devices []Device
	}{
		Serial:  r.URL.Query().Get("serial"),
		Devices: ws.getDevices(),
	}
	if err := templates.ExecuteTemplate(w, "devices", data); err != nil {
		slog.Error("template execution failed", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (ws *WebServer) handleLogs(w http.ResponseWriter, r *http.Request) {
	slog.Info("handleLogs", "url", r.URL)
	serial := r.URL.Query().Get("serial")

	if serial == "" {
		http.Error(w, "serial required", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	msgChan, unsubscribe := ws.riotServer.Subscribe()
	defer unsubscribe()

	slog.Info("subscribed to messages from", "serial", serial)
	for {
		select {
		case <-r.Context().Done():
			return
		case msg := <-msgChan:
			if msg.GetData() == nil {
				continue
			}
			var buf bytes.Buffer
			for _, data := range msg.GetData().Data {
				if data.GetSerialNumber() != serial {
					continue
				}
				for _, pt := range data.Data {
					log := pt.GetLog()
					if log == nil {
						continue
					}
					slog.Info("received log", "log", log)
					short, level := priorityToLevel(log.Priority)
					logData := struct {
						Timestamp  string
						Level      string
						ShortLevel string
						Message    string
					}{
						Timestamp:  formatTime(log.Timestamp),
						Level:      level,
						ShortLevel: short,
						Message:    log.Message,
					}
					if err := templates.ExecuteTemplate(&buf, "log", logData); err != nil {
						slog.Error("template execution failed", "err", err)
						return
					}
				}
			}
			if buf.Len() == 0 {
				continue
			}
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", strings.ReplaceAll(buf.String(), "\n", ""))
			w.(http.Flusher).Flush()
		}
	}
}

func priorityToLevel(p uint32) (string, string) {
	switch p {
	case 1:
		return "FTL", "fatal"
	case 2:
		return "ERR", "error"
	case 3:
		return "WRN", "warning"
	case 4:
		return "INF", "info"
	case 5:
		return "VRB", "verbose"
	case 6:
		return "DBG", "debug"
	default:
		return fmt.Sprintf("PRI%v", p), "unknown"
	}
}

func formatTime(ts uint64) string {
	t := time.UnixMilli(int64(ts))
	// Format according to time.RFC3339Nano since it is highly optimized,
	// but truncate it to use millisecond resolution.
	// Unfortunately, that format trims trailing 0s, so add 1/10 millisecond
	// to guarantee that there are exactly 4 digits after the period.
	const prefixLen = len("2006-01-02T15:04:05.000")
	b := make([]byte, 0, len(time.RFC3339Nano))
	n := len(b)
	t = t.Truncate(time.Millisecond).Add(time.Millisecond / 10)
	b = t.AppendFormat(b, time.RFC3339Nano)
	b = append(b[:n+prefixLen], b[n+prefixLen+1:]...) // drop the 4th digit
	return string(b)
}

func (ws *WebServer) handleCommand(w http.ResponseWriter, r *http.Request) {
	serial := r.URL.Query().Get("serial")
	if serial == "" {
		http.Error(w, "serial required", http.StatusBadRequest)
		return
	}
	resp := struct {
		Serial string
		Error  string
		Cmd    string
	}{
		Serial: serial,
		Error:  "",
	}
	sendForm := func() {
		if err := templates.ExecuteTemplate(w, "command_form", resp); err != nil {
			slog.Error("template execution failed", "err", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
	if err := r.ParseForm(); err != nil {
		resp.Error = "invalid form data"
		sendForm()
		return
	}
	cmds := r.Form["cmd"]
	if len(cmds) == 0 {
		resp.Error = "command required"
		sendForm()
		return
	}

	for _, cmd := range cmds {
		msg := &igtimi.Msg{
			Msg: &igtimi.Msg_DeviceManagement{
				DeviceManagement: &igtimi.DeviceManagement{
					Msg: &igtimi.DeviceManagement_Request{
						Request: &igtimi.DeviceManagementRequest{
							Msg: &igtimi.DeviceManagementRequest_Command{
								Command: &igtimi.DeviceCommand{
									Command: &igtimi.DeviceCommand_Text{
										Text: cmd,
									},
								},
							},
						},
					},
				},
			},
		}
		if err := ws.riotServer.Send(serial, msg); err != nil {
			resp.Error = err.Error()
			resp.Cmd = cmd // keep command in form on error
			break
		}
	}
	sendForm()
}
