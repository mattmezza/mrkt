package mcpserver

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"

	"github.com/mattmezza/mrkt/internal/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestProtocolHandshakeListAndCall(t *testing.T) {
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"items":[],"next_cursor":""}`)
	}))
	defer h.Close()
	api, _ := client.New(h.URL, "token")
	server := New(api)
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := server.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	c := mcp.NewClient(&mcp.Implementation{Name: "mrkt-test", Version: "1"}, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	listed, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 3 {
		t.Fatalf("got %d tools", len(listed.Tools))
	}
	for _, tool := range listed.Tools {
		if tool.InputSchema == nil {
			t.Fatalf("%s lacks schema", tool.Name)
		}
	}
	result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "mrkt_query", Arguments: map[string]any{"project": "demo", "resource": "deliveries"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("tool returned error: %+v", result)
	}
}

func TestStdioHelper(t *testing.T) {
	if os.Getenv("MRKT_MCP_HELPER") != "1" {
		return
	}
	api, _ := client.New("http://127.0.0.1:1", "test")
	if err := Serve(context.Background(), api); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestStdioSubprocessHandshake(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=TestStdioHelper")
	cmd.Env = append(os.Environ(), "MRKT_MCP_HELPER=1")
	c := mcp.NewClient(&mcp.Implementation{Name: "stdio-smoke", Version: "1"}, nil)
	ctx := context.Background()
	session, err := c.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) != 3 {
		t.Fatalf("got %d tools", len(listed.Tools))
	}
}
