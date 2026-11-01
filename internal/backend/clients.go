package backend

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

var errRemoteNotFound = errors.New("remote resource not found")

type operationKey struct{}
type operationMeta struct{ operation, dependency string }

func operationContext(ctx context.Context, operation, dependency string) context.Context {
	return context.WithValue(ctx, operationKey{}, operationMeta{operation, dependency})
}

type remoteClient struct {
	http *http.Client
	cfg  resolvedSettings
}

func newRemote(cfg resolvedSettings) *remoteClient {
	return &remoteClient{http: &http.Client{Timeout: time.Duration(cfg.Processing.RequestTimeoutSeconds) * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}, cfg: cfg}
}

// Only status codes are included in errors; provider bodies may contain credentials or input media.
func (c *remoteClient) request(ctx context.Context, method, address, key, authHeader string, body, out any) (resultErr error) {
	meta, _ := ctx.Value(operationKey{}).(operationMeta)
	started := time.Now()
	attempts := 0
	ctx = bindLog(ctx, slog.String("operation_id", uuid.NewString()), slog.String("dependency", meta.dependency), slog.String("operation", meta.operation))
	setStage(ctx, meta.operation)
	defer func() {
		result := "succeeded"
		if resultErr != nil {
			result = "failed"
		}
		if errors.Is(resultErr, errRemoteNotFound) {
			result = "not_found"
		}
		attrs := append(failureFields(resultErr), slog.String("result", result), slog.Int("attempts", attempts), slog.Int64("duration_ms", time.Since(started).Milliseconds()))
		emit(ctx, slog.LevelDebug, "dependency.finished", attrs...)
		if resultErr == nil {
			progressed(ctx)
		}
	}()
	var data []byte
	var err error
	if body != nil {
		data, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	for attempt := 0; attempt < 3; attempt++ {
		attempts = attempt + 1
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
				return fail(errRemoteNotFound, nil, "remote_not_found", meta.operation, meta.dependency, slog.Int("http_status", 404), slog.Int("attempts", attempts))
			}
			err = fail(fmt.Errorf("remote HTTP %d", resp.StatusCode), nil, "remote_http", meta.operation, meta.dependency, slog.Int("http_status", resp.StatusCode), slog.Int("attempts", attempts))
			retry = resp.StatusCode == 429 || resp.StatusCode >= 500
		} else {
			code := "network_error"
			var network net.Error
			if errors.As(callErr, &network) && network.Timeout() {
				code = "request_timeout"
			}
			err = fail(fmt.Errorf("remote connection failed"), callErr, code, meta.operation, meta.dependency, slog.Int("attempts", attempts))
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !retry || attempt == 2 {
			return err
		}
		delay := time.Duration(1<<attempt) * 250 * time.Millisecond
		emit(ctx, slog.LevelWarn, "dependency.retry", append(failureFields(err), slog.Int("attempt", attempts), slog.Int64("retry_delay_ms", delay.Milliseconds()))...)
		timer := time.NewTimer(delay)
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
	vectors, err := c.embedMany(ctx, input, 1)
	if err != nil {
		return nil, err
	}
	return vectors[0], nil
}

func (c *remoteClient) embedMany(ctx context.Context, input any, count int) ([][]float32, error) {
	var out struct {
		Data []struct {
			Index     *int      `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	err := c.request(ctx, "POST", c.cfg.EmbeddingURL+"/embeddings", c.cfg.EmbeddingKey, "Authorization", map[string]any{"model": c.cfg.Model, "input": input, "encoding_format": "float"}, &out)
	if err != nil {
		return nil, fmt.Errorf("embedding: %w", err)
	}
	if len(out.Data) != count {
		return nil, errors.New("embedding response count/index is invalid")
	}
	vectors := make([][]float32, count)
	for _, entry := range out.Data {
		if entry.Index == nil || *entry.Index < 0 || *entry.Index >= count || vectors[*entry.Index] != nil || len(entry.Embedding) == 0 {
			return nil, errors.New("embedding response count/index is invalid")
		}
		var norm float64
		for _, v := range entry.Embedding {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return nil, errors.New("embedding contains non-finite values")
			}
			norm += float64(v) * float64(v)
		}
		if norm == 0 {
			return nil, errors.New("embedding is a zero vector")
		}
		vectors[*entry.Index] = entry.Embedding
	}
	return vectors, nil
}
func (c *remoteClient) embedText(ctx context.Context, text string) ([]float32, error) {
	return c.embed(operationContext(ctx, "embedding.text", "embedding"), c.cfg.Embedding.QueryPrefix+text)
}
func (c *remoteClient) embedImage(ctx context.Context, jpeg []byte) ([]float32, error) {
	vectors, err := c.embedImages(ctx, [][]byte{jpeg})
	if err != nil {
		return nil, err
	}
	return vectors[0], nil
}

func (c *remoteClient) embedImages(ctx context.Context, images [][]byte) ([][]float32, error) {
	if len(images) == 0 {
		return nil, errors.New("embedding batch is empty")
	}
	input := make([]any, len(images))
	for i, jpeg := range images {
		input[i] = map[string]any{"content": []any{map[string]any{"type": "image_url", "image_url": map[string]string{"url": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpeg)}}}}
	}
	ctx = bindLog(ctx, slog.Int("batch_size", len(images)))
	return c.embedMany(operationContext(ctx, "embedding.image", "embedding"), input, len(images))
}
func (c *remoteClient) models(ctx context.Context) (any, error) {
	ctx = operationContext(ctx, "embedding.models", "embedding")
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	err := c.request(ctx, "GET", c.cfg.EmbeddingURL+"/models", c.cfg.EmbeddingKey, "Authorization", nil, &out)
	return out, err
}
func (c *remoteClient) qdrant(ctx context.Context, method, path string, body, out any) error {
	operation := "qdrant.collection"
	switch {
	case strings.HasSuffix(path, "/points/query"):
		operation = "qdrant.query"
	case strings.HasSuffix(path, "/points/delete"):
		operation = "qdrant.delete"
	case strings.HasSuffix(path, "/points"):
		operation = "qdrant.upsert"
	case strings.Contains(path, "/points/"):
		operation = "qdrant.point"
	}
	ctx = operationContext(ctx, operation, "qdrant")
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
