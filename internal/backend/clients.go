package backend

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"time"
)

var errRemoteNotFound = errors.New("remote resource not found")

type remoteClient struct {
	http *http.Client
	cfg  resolvedSettings
}

func newRemote(cfg resolvedSettings) *remoteClient {
	return &remoteClient{http: &http.Client{Timeout: time.Duration(cfg.Processing.RequestTimeoutSeconds) * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, cfg: cfg}
}

// Only status codes are included in errors; provider bodies may contain credentials or input media.
func (c *remoteClient) request(ctx context.Context, method, address, key, authHeader string, body, out any) error {
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, address, bytes.NewReader(data))
		if err != nil {
			return err
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if key != "" {
			headerValue := key
			if authHeader == "Authorization" {
				headerValue = "Bearer " + key
			}
			req.Header.Set(authHeader, headerValue)
		}
		resp, callErr := c.http.Do(req)
		retry := callErr != nil
		if callErr == nil {
			limited := io.LimitReader(resp.Body, 16<<20)
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				if out != nil {
					err = json.NewDecoder(limited).Decode(out)
				} else {
					_, err = io.Copy(io.Discard, limited)
				}
				_ = resp.Body.Close()
				return err
			}
			_, _ = io.Copy(io.Discard, limited)
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusNotFound {
				return errRemoteNotFound
			}
			err = fmt.Errorf("remote HTTP %d", resp.StatusCode)
			retry = resp.StatusCode == 429 || resp.StatusCode >= 500
		} else {
			err = fmt.Errorf("remote connection failed")
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !retry || attempt == 2 {
			return err
		}
		timer := time.NewTimer(time.Duration(1<<attempt) * 250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return err
}

func (c *remoteClient) embed(ctx context.Context, input any) ([]float32, error) {
	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	err := c.request(ctx, "POST", c.cfg.EmbeddingURL+"/embeddings", c.cfg.EmbeddingKey, "Authorization", map[string]any{"model": c.cfg.Model, "input": input, "encoding_format": "float"}, &out)
	if err != nil {
		return nil, fmt.Errorf("embedding: %w", err)
	}
	if len(out.Data) != 1 || out.Data[0].Index != 0 || len(out.Data[0].Embedding) == 0 {
		return nil, errors.New("embedding response count/index is invalid")
	}
	vector := out.Data[0].Embedding
	var norm float64
	for _, v := range vector {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return nil, errors.New("embedding contains non-finite values")
		}
		norm += float64(v) * float64(v)
	}
	if norm == 0 {
		return nil, errors.New("embedding is a zero vector")
	}
	return vector, nil
}
func (c *remoteClient) embedText(ctx context.Context, text string) ([]float32, error) {
	return c.embed(ctx, c.cfg.Embedding.QueryPrefix+text)
}
func (c *remoteClient) embedImage(ctx context.Context, jpeg []byte) ([]float32, error) {
	return c.embed(ctx, []any{map[string]any{"content": []any{map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpeg)}}}}})
}
func (c *remoteClient) models(ctx context.Context) (any, error) {
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	err := c.request(ctx, "GET", c.cfg.EmbeddingURL+"/models", c.cfg.EmbeddingKey, "Authorization", nil, &out)
	return out, err
}
func (c *remoteClient) qdrant(ctx context.Context, method, path string, body, out any) error {
	return c.request(ctx, method, c.cfg.QdrantURL+path, c.cfg.QdrantKey, "api-key", body, out)
}
func collectionPath(name string) string { return "/collections/" + url.PathEscape(name) }
func (c *remoteClient) ensureCollection(ctx context.Context, name string, dim int) error {
	var out struct {
		Result struct {
			Config struct {
				Params struct {
					Vectors struct {
						Size     int    `json:"size"`
						Distance string `json:"distance"`
					} `json:"vectors"`
				} `json:"params"`
			} `json:"config"`
		} `json:"result"`
	}
	err := c.qdrant(ctx, "GET", collectionPath(name), nil, &out)
	if errors.Is(err, errRemoteNotFound) {
		return c.qdrant(ctx, "PUT", collectionPath(name), map[string]any{"vectors": map[string]any{"size": dim, "distance": "Cosine"}}, nil)
	}
	if err != nil {
		return err
	}
	if out.Result.Config.Params.Vectors.Size != dim || out.Result.Config.Params.Vectors.Distance != "Cosine" {
		return errors.New("qdrant collection dimension/distance mismatch")
	}
	return nil
}
func (c *remoteClient) upsert(ctx context.Context, collection, id string, vector []float32, payload any) error {
	return c.qdrant(ctx, "PUT", collectionPath(collection)+"/points?wait=true", map[string]any{"points": []any{map[string]any{"id": id, "vector": vector, "payload": payload}}}, nil)
}
func (c *remoteClient) deletePoints(ctx context.Context, collection string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	err := c.qdrant(ctx, "POST", collectionPath(collection)+"/points/delete?wait=true", map[string]any{"points": ids}, nil)
	if errors.Is(err, errRemoteNotFound) {
		return nil
	}
	return err
}

type vectorHit struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
}

func (c *remoteClient) vector(ctx context.Context, collection, id string) ([]float32, error) {
	var out struct {
		Result struct {
			Vector []float32 `json:"vector"`
		} `json:"result"`
	}
	err := c.qdrant(ctx, "GET", collectionPath(collection)+"/points/"+id+"?with_vector=true", nil, &out)
	if err == nil && len(out.Result.Vector) == 0 {
		err = errors.New("stored vector is empty")
	}
	return out.Result.Vector, err
}

func (c *remoteClient) query(ctx context.Context, collection string, vector []float32, source, kind string, limit, offset int) ([]vectorHit, error) {
	body := map[string]any{"query": vector, "limit": limit, "offset": offset, "with_payload": false}
	conditions := []any{}
	for key, value := range map[string]string{"source_id": source, "media_type": kind} {
		if value != "" {
			conditions = append(conditions, map[string]any{"key": key, "match": map[string]string{"value": value}})
		}
	}
	if len(conditions) > 0 {
		body["filter"] = map[string]any{"must": conditions}
	}
	var out struct {
		Result struct {
			Points []vectorHit `json:"points"`
		} `json:"result"`
	}
	err := c.qdrant(ctx, "POST", collectionPath(collection)+"/points/query", body, &out)
	return out.Result.Points, err
}
