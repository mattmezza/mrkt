// Package client implements the supported mrkt REST API client.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

type Error struct {
	Code, Message string
	Status        int
}

func (e *Error) Error() string { return fmt.Sprintf("mrkt API: %s: %s", e.Code, e.Message) }

type Operation struct {
	Project, Resource, Action, ID, Key string
	Input                              any
	Limit                              int
	Cursor                             string
}

func New(baseURL, token string) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("invalid mrkt server URL %q", baseURL)
	}
	return &Client{BaseURL: baseURL, Token: token, HTTP: http.DefaultClient}, nil
}

func (c *Client) Do(ctx context.Context, op Operation) (json.RawMessage, error) {
	method, path, err := route(op)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if op.Input != nil {
		b, err := json.Marshal(op.Input)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	u := c.BaseURL + path
	if method == http.MethodGet && (op.Limit != 0 || op.Cursor != "") {
		q := url.Values{}
		if op.Limit != 0 {
			q.Set("limit", strconv.Itoa(op.Limit))
		}
		if op.Cursor != "" {
			q.Set("cursor", op.Cursor)
		}
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if op.Key != "" {
		req.Header.Set("Idempotency-Key", op.Key)
	}
	return c.send(req)
}

func (c *Client) PutArtifact(ctx context.Context, project, hash, contentType string, size int64, body io.Reader) error {
	path := "/api/v1/projects/" + url.PathEscape(project) + "/artifacts/" + url.PathEscape(hash)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", contentType)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	_, err = c.send(req)
	return err
}

// Delete removes one resource through the standard project resource route.
func (c *Client) Delete(ctx context.Context, project, resource, id, key string) error {
	if project == "" || resource == "" || id == "" {
		return fmt.Errorf("project, resource and id are required")
	}
	p := "/api/v1/projects/" + url.PathEscape(project) + "/" + url.PathEscape(resource) + "/" + url.PathEscape(id)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.BaseURL+p, nil)
	if err != nil {
		return err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	_, err = c.send(req)
	return err
}

func (c *Client) send(req *http.Request) (json.RawMessage, error) {
	h := c.HTTP
	if h == nil {
		h = http.DefaultClient
	}
	res, err := h.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(b, &env) != nil || env.Error.Code == "" {
			return nil, &Error{Code: "http_error", Message: strings.TrimSpace(string(b)), Status: res.StatusCode}
		}
		return nil, &Error{Code: env.Error.Code, Message: env.Error.Message, Status: res.StatusCode}
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return json.RawMessage(`null`), nil
	}
	if !json.Valid(b) {
		return nil, fmt.Errorf("mrkt API returned invalid JSON")
	}
	return json.RawMessage(b), nil
}

func route(op Operation) (string, string, error) {
	esc := url.PathEscape
	if op.Project == "" && op.Resource == "projects" {
		if op.Action != "" || op.ID != "" {
			return "", "", fmt.Errorf("invalid projects operation")
		}
		if op.Input != nil {
			return http.MethodPost, "/api/v1/projects", nil
		}
		return http.MethodGet, "/api/v1/projects", nil
	}
	if op.Project == "" && op.Resource == "installation" {
		if op.Action != "" {
			return http.MethodPost, "/api/v1/installation/" + esc(op.Action), nil
		}
		return http.MethodGet, "/api/v1/installation", nil
	}
	if op.Project == "" || op.Resource == "" {
		return "", "", fmt.Errorf("project and resource are required")
	}
	p := "/api/v1/projects/" + esc(op.Project) + "/" + esc(op.Resource)
	if op.ID == "" {
		if op.Input != nil {
			return http.MethodPost, p, nil
		}
		return http.MethodGet, p, nil
	}
	p += "/" + esc(op.ID)
	if op.Action != "" {
		return http.MethodPost, p + "/" + esc(op.Action), nil
	}
	if op.Input != nil {
		return "", "", fmt.Errorf("resource create must omit id")
	}
	return http.MethodGet, p, nil
}
