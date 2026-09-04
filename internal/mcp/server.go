package mcp

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// Server coordinates MCP protocol handling and dispatch.
type Server struct {
	registry *ToolRegistry

	// SSE session tracking
	mu       sync.RWMutex
	sessions map[string]chan []byte
}

// NewServer creates an MCP server with the given tool registry.
func NewServer(registry *ToolRegistry) *Server {
	return &Server{
		registry: registry,
		sessions: make(map[string]chan []byte),
	}
}

// ServeStdio runs the MCP JSON-RPC event loop over standard IO until EOF or context cancellation.
func (s *Server) ServeStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	// Allow large responses (e.g., historical analogs / snapshots) up to 10MB
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}

		respBytes, err := s.ProcessMessage(ctx, line)
		if err != nil {
			log.Warn().Err(err).Msg("process MCP message error")
		}
		if len(respBytes) > 0 {
			if _, err := out.Write(respBytes); err != nil {
				return fmt.Errorf("write response: %w", err)
			}
			if _, err := out.Write([]byte("\n")); err != nil {
				return fmt.Errorf("write newline: %w", err)
			}
		}
	}

	return scanner.Err()
}

// ProcessMessage processes a raw JSON-RPC 2.0 message and returns the serialized response.
// Returns (nil, nil) for notifications that require no response.
func (s *Server) ProcessMessage(ctx context.Context, raw []byte) ([]byte, error) {
	var req JSONRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return s.errorResponse(nil, CodeParseError, "Parse error: "+err.Error()), nil
	}

	if req.JSONRPC != "2.0" || req.Method == "" {
		return s.errorResponse(req.ID, CodeInvalidRequest, "Invalid Request: jsonrpc must be '2.0' and method non-empty"), nil
	}

	// Notifications (no ID)
	if req.ID == nil {
		s.handleNotification(ctx, req.Method, req.Params)
		return nil, nil
	}

	switch req.Method {
	case "initialize":
		result := InitializeResult{
			ProtocolVersion: "2024-11-05",
			Capabilities: ServerCapabilities{
				Tools: &ToolCapabilities{ListChanged: false},
			},
			ServerInfo: Implementation{
				Name:    "sonde-mcp",
				Version: "0.1.0",
			},
		}
		return s.successResponse(req.ID, result), nil

	case "ping":
		return s.successResponse(req.ID, map[string]any{}), nil

	case "tools/list":
		result := ListToolsResult{
			Tools: ReadOnlyTools,
		}
		return s.successResponse(req.ID, result), nil

	case "tools/call":
		var params CallToolParams
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return s.errorResponse(req.ID, CodeInvalidParams, "Invalid params: "+err.Error()), nil
			}
		}
		if params.Name == "" {
			return s.errorResponse(req.ID, CodeInvalidParams, "Tool name is required"), nil
		}

		// Ensure tool is registered in ReadOnlyTools
		var found bool
		for _, t := range ReadOnlyTools {
			if t.Name == params.Name {
				found = true
				break
			}
		}
		if !found {
			return s.successResponse(req.ID, CallToolResult{
				Content: []ContentItem{
					{Type: "text", Text: fmt.Sprintf("Error: tool %q is not found or not permitted (only read-only tools are exposed)", params.Name)},
				},
				IsError: true,
			}), nil
		}

		out, err := s.registry.Execute(ctx, params.Name, params.Arguments)
		if err != nil {
			return s.successResponse(req.ID, CallToolResult{
				Content: []ContentItem{
					{Type: "text", Text: fmt.Sprintf("Error executing %s: %v", params.Name, err)},
				},
				IsError: true,
			}), nil
		}

		return s.successResponse(req.ID, CallToolResult{
			Content: []ContentItem{
				{Type: "text", Text: out},
			},
			IsError: false,
		}), nil

	default:
		return s.errorResponse(req.ID, CodeMethodNotFound, fmt.Sprintf("Method not found: %q", req.Method)), nil
	}
}

func (s *Server) handleNotification(ctx context.Context, method string, params json.RawMessage) {
	switch method {
	case "notifications/initialized":
		log.Debug().Msg("MCP client sent initialized notification")
	default:
		log.Debug().Str("method", method).Msg("unhandled MCP notification")
	}
}

func (s *Server) successResponse(id *json.RawMessage, result any) []byte {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
	bytes, _ := json.Marshal(resp)
	return bytes
}

func (s *Server) errorResponse(id *json.RawMessage, code int, msg string) []byte {
	resp := JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &JSONRPCError{
			Code:    code,
			Message: msg,
		},
	}
	bytes, _ := json.Marshal(resp)
	return bytes
}

// ServeHTTP serves MCP over HTTP/SSE.
func (s *Server) ServeHTTP(ctx context.Context, addr string, apiToken string) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		if !checkAuth(r, apiToken) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		sessionID := randomHex(16)
		ch := make(chan []byte, 32)
		s.mu.Lock()
		s.sessions[sessionID] = ch
		s.mu.Unlock()

		defer func() {
			s.mu.Lock()
			delete(s.sessions, sessionID)
			close(ch)
			s.mu.Unlock()
		}()

		// Send initial endpoint event per MCP SSE spec
		endpointURL := fmt.Sprintf("/message?session_id=%s", sessionID)
		_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", endpointURL)
		flusher.Flush()

		notify := r.Context().Done()
		for {
			select {
			case <-notify:
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", string(msg))
				flusher.Flush()
			}
		}
	})

	mux.HandleFunc("/message", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !checkAuth(r, apiToken) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Read error", http.StatusBadRequest)
			return
		}

		sessionID := r.URL.Query().Get("session_id")
		respBytes, _ := s.ProcessMessage(r.Context(), body)

		if sessionID != "" {
			s.mu.RLock()
			ch, exists := s.sessions[sessionID]
			s.mu.RUnlock()
			if exists && len(respBytes) > 0 {
				ch <- respBytes
				w.WriteHeader(http.StatusAccepted)
				return
			}
		}

		// Direct HTTP POST response fallback
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(respBytes)
	})

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()

	log.Info().Str("addr", addr).Msg("starting MCP HTTP/SSE listener")
	return server.ListenAndServe()
}

func checkAuth(r *http.Request, token string) bool {
	if token == "" {
		return true
	}
	auth := r.Header.Get("Authorization")
	parts := strings.SplitN(auth, " ", 2)
	return len(parts) == 2 && strings.EqualFold(parts[0], "bearer") && parts[1] == token
}

func randomHex(bytesLen int) string {
	b := make([]byte, bytesLen)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
