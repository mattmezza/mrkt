// Package mcpserver exposes the mrkt REST API as MCP tools.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mattmezza/mrkt/internal/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Server struct{ API *client.Client }

type QueryInput struct {
	Project  string `json:"project,omitempty" jsonschema:"Project identifier; omit only for installation or projects"`
	Resource string `json:"resource" jsonschema:"REST resource such as contacts, releases, deliveries, or projects"`
	ID       string `json:"id,omitempty" jsonschema:"Resource identifier for a get"`
	Limit    int    `json:"limit,omitempty" jsonschema:"Maximum items from 1 to 200"`
	Cursor   string `json:"cursor,omitempty" jsonschema:"Opaque pagination cursor from the previous response"`
}
type MutateInput struct {
	Project        string         `json:"project,omitempty" jsonschema:"Project identifier"`
	Resource       string         `json:"resource" jsonschema:"REST resource"`
	Action         string         `json:"action,omitempty" jsonschema:"Explicit operation such as pause, resume, cancel, explain, simulate, plan, deploy, or rollback"`
	ID             string         `json:"id,omitempty" jsonschema:"Resource identifier; use _ for singleton actions"`
	IdempotencyKey string         `json:"idempotency_key,omitempty" jsonschema:"Stable key for retry-safe mutations"`
	Input          map[string]any `json:"input,omitempty" jsonschema:"Operation-specific JSON request body"`
}
type ToolOutput struct {
	Result any `json:"result" jsonschema:"Decoded mrkt API response"`
}
type MetadataInput struct{}
type MetadataOutput struct {
	Resources []string `json:"resources"`
	Actions   []string `json:"actions"`
	Notes     []string `json:"notes"`
}

func Serve(ctx context.Context, api *client.Client) error {
	s := New(api)
	return s.Run(ctx, &mcp.StdioTransport{})
}

func New(api *client.Client) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "mrkt", Version: "0.1.0"}, nil)
	readOnly := true
	destructive := false
	mcp.AddTool(s, &mcp.Tool{Name: "mrkt_metadata", Description: "Describe supported mrkt resources, explicit actions, and safety rules.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &destructive}}, func(context.Context, *mcp.CallToolRequest, MetadataInput) (*mcp.CallToolResult, MetadataOutput, error) {
		return nil, MetadataOutput{Resources: []string{"installation", "projects", "tokens", "contacts", "lists", "releases", "artifacts", "events", "enrollments", "broadcasts", "deliveries", "webhooks", "webhook-deliveries", "domains", "transports", "consent", "operations"}, Actions: []string{"plan", "deploy", "rollback", "preview", "simulate", "import", "preview-import", "export", "confirm", "unsubscribe", "preferences", "pause", "resume", "cancel", "explain", "replay", "usage", "gc", "retention", "verify", "rotate"}, Notes: []string{"Use _ for singleton action IDs.", "Reuse an idempotency key only for an identical retry.", "Consent and suppression are live state."}}, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "mrkt_query", Description: "List or inspect mrkt projects, contacts, releases, deliveries, domains, and operational state.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly, DestructiveHint: &destructive}}, func(ctx context.Context, _ *mcp.CallToolRequest, in QueryInput) (*mcp.CallToolResult, ToolOutput, error) {
		raw, err := api.Do(ctx, client.Operation{Project: in.Project, Resource: in.Resource, ID: in.ID, Limit: in.Limit, Cursor: in.Cursor})
		if err != nil {
			return nil, ToolOutput{}, err
		}
		v, err := decode(raw)
		return nil, ToolOutput{Result: v}, err
	})
	mcp.AddTool(s, &mcp.Tool{Name: "mrkt_operate", Description: "Create a resource or run an explicit mrkt operation. Supply an idempotency key for retryable mutations.", Annotations: &mcp.ToolAnnotations{ReadOnlyHint: destructive}}, func(ctx context.Context, _ *mcp.CallToolRequest, in MutateInput) (*mcp.CallToolResult, ToolOutput, error) {
		if in.Action != "" && in.ID == "" {
			in.ID = "_"
		}
		raw, err := api.Do(ctx, client.Operation{Project: in.Project, Resource: in.Resource, ID: in.ID, Action: in.Action, Key: in.IdempotencyKey, Input: in.Input})
		if err != nil {
			return nil, ToolOutput{}, err
		}
		v, err := decode(raw)
		return nil, ToolOutput{Result: v}, err
	})
	return s
}

func decode(raw []byte) (any, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("decode mrkt response: %w", err)
	}
	return v, nil
}
