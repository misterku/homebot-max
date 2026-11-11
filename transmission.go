package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type TransmissionClient struct {
	endpoint string
	username string
	password string
	http     *http.Client

	mu        sync.Mutex
	sessionID string
}

// Torrent represents a torrent as returned by the Transmission RPC torrent-get method.
type Torrent struct {
	ID         int64 `json:"id"`
	Status     *int  `json:"status"`
	IsFinished *bool `json:"isFinished"`
}

type rpcRequest struct {
	Method    string      `json:"method"`
	Arguments interface{} `json:"arguments"`
}

type rpcResponse struct {
	Result    string          `json:"result"`
	Arguments json.RawMessage `json:"arguments"`
}

func NewTransmissionClient(username, password, host, scheme string, port int) *TransmissionClient {
	endpoint := fmt.Sprintf("%s://%s:%d/transmission/rpc", scheme, host, port)
	return &TransmissionClient{
		endpoint: endpoint,
		username: username,
		password: password,
		http: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// call performs a raw RPC call, transparently handling the X-Transmission-Session-Id handshake.
func (c *TransmissionClient) call(ctx context.Context, method string, args interface{}) (json.RawMessage, error) {
	// The session-ID handshake mutates c.sessionID, so serialize calls.
	c.mu.Lock()
	defer c.mu.Unlock()

	req, err := c.buildRequest(ctx, method, args)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}

	// Transmission returns 409 with a session ID header until the client sends it.
	if resp.StatusCode == http.StatusConflict {
		c.sessionID = resp.Header.Get("X-Transmission-Session-Id")
		resp.Body.Close()

		req, err = c.buildRequest(ctx, method, args)
		if err != nil {
			return nil, err
		}
		resp, err = c.http.Do(req)
		if err != nil {
			return nil, err
		}
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("transmission RPC returned status %d", resp.StatusCode)
	}

	var body rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if body.Result != "success" {
		return nil, fmt.Errorf("transmission RPC error: %s", body.Result)
	}
	return body.Arguments, nil
}

func (c *TransmissionClient) buildRequest(ctx context.Context, method string, args interface{}) (*http.Request, error) {
	payload := rpcRequest{Method: method, Arguments: args}
	buf, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.username != "" || c.password != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	if c.sessionID != "" {
		req.Header.Set("X-Transmission-Session-Id", c.sessionID)
	}
	return req, nil
}

// TorrentGet returns torrent fields matching the given arguments.
func (c *TransmissionClient) TorrentGet(ctx context.Context, fields []string, anyParam map[string]interface{}) ([]Torrent, error) {
	args := map[string]interface{}{
		"fields": fields,
	}
	for k, v := range anyParam {
		args[k] = v
	}

	raw, err := c.call(ctx, "torrent-get", args)
	if err != nil {
		return nil, err
	}

	var result struct {
		Torrents []Torrent `json:"torrents"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result.Torrents, nil
}

// TorrentRemove removes the torrents with the given IDs.
func (c *TransmissionClient) TorrentRemove(ctx context.Context, ids []int64, deleteLocalData bool) error {
	args := map[string]interface{}{
		"ids":               ids,
		"delete-local-data": deleteLocalData,
	}
	_, err := c.call(ctx, "torrent-remove", args)
	return err
}

// TorrentAddFile adds a torrent from a locally stored .torrent file. The file
// contents are sent to Transmission base64-encoded in the metainfo argument.
func (c *TransmissionClient) TorrentAddFile(ctx context.Context, filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	args := map[string]interface{}{
		"metainfo": base64.StdEncoding.EncodeToString(data),
		"paused":   false,
	}
	_, err = c.call(ctx, "torrent-add", args)
	return err
}

// Ping verifies connectivity with the Transmission RPC server.
func (c *TransmissionClient) Ping(ctx context.Context) error {
	_, err := c.call(ctx, "session-get", map[string]interface{}{})
	return err
}

func SendFileToTransmission(client *TransmissionClient, fileID string) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	filename := filepath.Join(dir, "downloads", fileID+".torrent")
	return client.TorrentAddFile(context.TODO(), filename)
}
