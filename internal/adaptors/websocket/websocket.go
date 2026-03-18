package websocket

// Package websocket provides a thin adaptor that exposes the core
// TLV-based session over WebSocket. Each connected client gets its
// own agent session wired to a ChanInput/Output pair; the adaptor
// is responsible only for upgrading HTTP, shuttling TLV bytes, and
// serving the embedded HTML chat UI.

import (
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	_ "embed"

	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/stream"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(_ *http.Request) bool {
		return true
	},
}

// Adaptor connects WebSocket clients to agent sessions.
type Adaptor struct {
	Config *app.Config
	Server *http.Server
}

// NewAdaptor creates a WebSocket server. Each client gets its own agent session.
func NewAdaptor(port string, cfg *app.Config) *Adaptor {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", handleWebSocket(cfg))
	mux.HandleFunc("/", serveIndex)

	return &Adaptor{
		Config: cfg,
		Server: &http.Server{
			Addr:              port,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		},
	}
}

// Start begins listening in a goroutine.
func (a *Adaptor) Start() {
	go a.Server.ListenAndServe() //nolint:errcheck // server runs in background
}

// serveIndex serves the embedded chat UI.
func serveIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(indexHTML) //nolint:errcheck // static HTML, write error not critical
}

// handleWebSocket upgrades HTTP to WebSocket and runs a session.
func handleWebSocket(cfg *app.Config) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		input := stream.NewChanInput(100)
		defer input.Close() // Signal session's readFromInput to exit

		output := newClientOutput(conn)

		// Each connection gets its own agent session.
		agentpkg.LoadOrNewSession(cfg.AgentTools, cfg.SystemPrompt, cfg.ExtraSystemPrompt, cfg.MaxSteps, input, output, cfg.Cfg.Session, cfg.Cfg.ModelConfig, cfg.Cfg.RuntimeConfig, cfg.Cfg.DebugAPI, cfg.Cfg.Proxy)

		readMessages(conn, input)
	}
}

// readMessages reads TLV messages from conn and forwards to input.
func readMessages(conn *websocket.Conn, input *stream.ChanInput) {
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if len(message) == 0 {
			continue
		}

		// Filter out :quit and :q commands from web client.
		if tag, value, ok := parseTLV(message); ok && tag == stream.TagTextUser {
			if value == ":quit" || value == ":q" {
				continue
			}
		}

		_ = input.Emit(message) //nolint:errcheck // best-effort WebSocket message
	}
}

// parseTLV extracts tag and value from a TLV-encoded message.
// Returns ok=false if message is too short.
func parseTLV(message []byte) (tag string, value string, ok bool) {
	if len(message) < 6 {
		return "", "", false
	}
	tag = string(message[0:2])
	length := uint32(message[2])<<24 | uint32(message[3])<<16 | uint32(message[4])<<8 | uint32(message[5])
	if len(message) < 6+int(length) {
		return "", "", false
	}
	return tag, string(message[6 : 6+length]), true
}

// clientOutput implements stream.Output for a WebSocket connection.
type clientOutput struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func newClientOutput(conn *websocket.Conn) *clientOutput {
	return &clientOutput{conn: conn}
}

func (o *clientOutput) Write(p []byte) (n int, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err = o.conn.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (o *clientOutput) WriteString(s string) (int, error) {
	return o.Write([]byte(s))
}

func (o *clientOutput) Flush() error { return nil }

//go:embed chat.html
var indexHTML []byte
