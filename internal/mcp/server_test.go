package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestMCP_Initialize(t *testing.T) {
	reg := NewToolRegistry(nil)
	server := NewServer(reg)

	req := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	respBytes, err := server.ProcessMessage(context.Background(), []byte(req))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %v", resp.Error)
	}

	initResult, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", resp.Result)
	}

	if initResult["protocolVersion"] != "2024-11-05" {
		t.Errorf("expected protocolVersion 2024-11-05, got %v", initResult["protocolVersion"])
	}
}

func TestMCP_ToolsList_StrictlyReadOnly(t *testing.T) {
	reg := NewToolRegistry(nil)
	server := NewServer(reg)

	req := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`
	respBytes, err := server.ProcessMessage(context.Background(), []byte(req))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	resultMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", resp.Result)
	}

	rawTools, ok := resultMap["tools"].([]any)
	if !ok {
		t.Fatalf("expected tools array, got %T", resultMap["tools"])
	}

	if len(rawTools) != 4 {
		t.Errorf("expected exactly 4 tools, got %d", len(rawTools))
	}

	// Forbidden mutation substrings in tool names
	forbidden := []string{
		"backfill", "sync", "write", "post", "delete", "remove",
		"update", "patch", "create", "insert", "accept", "reject",
	}

	expectedTools := map[string]bool{
		"list_active_alerts":       false,
		"get_research_snapshot":    false,
		"list_ontology_relations":  false,
		"list_ontology_candidates": false,
	}

	for _, rt := range rawTools {
		tm, ok := rt.(map[string]any)
		if !ok {
			t.Fatalf("expected tool map, got %T", rt)
		}
		name, _ := tm["name"].(string)

		if _, expected := expectedTools[name]; expected {
			expectedTools[name] = true
		} else {
			t.Errorf("unexpected tool registered: %q", name)
		}

		lower := strings.ToLower(name)
		for _, f := range forbidden {
			if strings.Contains(lower, f) {
				t.Errorf("forbidden mutation keyword %q found in tool name %q", f, name)
			}
		}
	}

	for name, found := range expectedTools {
		if !found {
			t.Errorf("expected tool %q was not found in tools/list", name)
		}
	}
}

func TestMCP_ToolsCall_RejectsMutationAndUnknownTools(t *testing.T) {
	reg := NewToolRegistry(nil)
	server := NewServer(reg)

	mutationAttempts := []string{
		"backfill",
		"sync",
		"create_relation",
		"accept_candidate",
		"unknown_tool",
	}

	for i, name := range mutationAttempts {
		reqMap := map[string]any{
			"jsonrpc": "2.0",
			"id":      10 + i,
			"method":  "tools/call",
			"params": map[string]any{
				"name": name,
			},
		}
		raw, _ := json.Marshal(reqMap)

		respBytes, err := server.ProcessMessage(context.Background(), raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		var resp JSONRPCResponse
		if err := json.Unmarshal(respBytes, &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}

		resMap, ok := resp.Result.(map[string]any)
		if !ok {
			t.Fatalf("expected CallToolResult map, got %T", resp.Result)
		}

		if resMap["isError"] != true {
			t.Errorf("expected isError=true for tool %q, got %v", name, resMap["isError"])
		}

		content, _ := resMap["content"].([]any)
		if len(content) == 0 {
			t.Errorf("expected error content for tool %q", name)
		}
	}
}

func TestMCP_ToolsCall_ExecuteSuccessWithOverride(t *testing.T) {
	reg := NewToolRegistry(nil)
	reg.overrides["get_research_snapshot"] = func(ctx context.Context, args map[string]any) (string, error) {
		alertID := args["alert_id"]
		return `{"alert_id":"` + alertID.(string) + `","trend":"bearish"}`, nil
	}
	server := NewServer(reg)

	req := `{"jsonrpc":"2.0","id":42,"method":"tools/call","params":{"name":"get_research_snapshot","arguments":{"alert_id":"alt_123"}}}`
	respBytes, err := server.ProcessMessage(context.Background(), []byte(req))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	resMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected CallToolResult, got %T", resp.Result)
	}

	if resMap["isError"] == true {
		t.Fatalf("expected isError=false, got %v", resMap)
	}

	content := resMap["content"].([]any)
	first := content[0].(map[string]any)
	text := first["text"].(string)
	if !strings.Contains(text, "alt_123") {
		t.Errorf("expected text to contain alt_123, got %q", text)
	}
}

func TestMCP_NotificationsIgnored(t *testing.T) {
	reg := NewToolRegistry(nil)
	server := NewServer(reg)

	notification := `{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`
	resp, err := server.ProcessMessage(context.Background(), []byte(notification))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != nil {
		t.Errorf("expected nil response for notification, got %s", string(resp))
	}
}

func TestMCP_ServeStdio(t *testing.T) {
	reg := NewToolRegistry(nil)
	server := NewServer(reg)

	input := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{}}` + "\n"
	in := bytes.NewBufferString(input)
	var out bytes.Buffer

	if err := server.ServeStdio(context.Background(), in, &out); err != nil {
		t.Fatalf("ServeStdio failed: %v", err)
	}

	var resp JSONRPCResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v, raw: %s", err, out.String())
	}
	if resp.Error != nil {
		t.Fatalf("expected no error, got %v", resp.Error)
	}
}
