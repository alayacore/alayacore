package websocket

import (
	"net/http"
	"strings"
	"sync"

	"github.com/gorilla/websocket"

	_ "embed"

	"github.com/alayacore/alayacore/internal/adaptors/common"
	agentpkg "github.com/alayacore/alayacore/internal/agent"
	"github.com/alayacore/alayacore/internal/app"
	"github.com/alayacore/alayacore/internal/stream"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// WebSocketAdaptor connects WebSocket clients to agent sessions.
type WebSocketAdaptor struct {
	Config *app.Config
	Server *http.Server
}

// NewWebSocketAdaptor creates a WebSocket server. Each client gets its own agent session.
func NewWebSocketAdaptor(port string, cfg *app.Config) *WebSocketAdaptor {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", handleWebSocket(cfg))
	mux.HandleFunc("/", serveIndex)

	return &WebSocketAdaptor{
		Config: cfg,
		Server: &http.Server{Addr: port, Handler: mux},
	}
}

// Start begins listening in a goroutine.
func (a *WebSocketAdaptor) Start() {
	go a.Server.ListenAndServe()
}

// serveIndex serves the embedded chat UI.
func serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	html := strings.Replace(string(indexHTML), "{{welcome}}", common.WelcomeText, 1)
	w.Write([]byte(html))
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
		agentpkg.LoadOrNewSession(cfg.Model, cfg.AgentTools, cfg.SystemPrompt, "", "", input, output, cfg.Cfg.Session, cfg.Cfg.ContextLimit, cfg.Cfg.ModelConfig, cfg.Cfg.RuntimeConfig)

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
		if tag, value, ok := parseTLV(message); ok && tag == stream.TagUserText {
			if value == ":quit" || value == ":q" {
				continue
			}
		}

		input.Emit(message)
	}
}

// parseTLV extracts tag and value from a TLV-encoded message.
// Returns ok=false if message is too short.
func parseTLV(message []byte) (tag byte, value string, ok bool) {
	if len(message) < 5 {
		return 0, "", false
	}
	tag = message[0]
	length := uint32(message[1])<<24 | uint32(message[2])<<16 | uint32(message[3])<<8 | uint32(message[4])
	if len(message) < 5+int(length) {
		return 0, "", false
	}
	return tag, string(message[5 : 5+length]), true
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
