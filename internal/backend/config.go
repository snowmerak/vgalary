package backend

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// Settings contains stored values and environment variable names, never API keys.
type Settings struct {
	Embedding  EmbeddingSettings  `json:"embedding"`
	Qdrant     QdrantSettings     `json:"qdrant"`
	Processing ProcessingSettings `json:"processing"`
}
type EmbeddingSettings struct {
	BaseURL       string `json:"base_url"`
	BaseURLEnv    string `json:"base_url_env"`
	Model         string `json:"model"`
	ModelEnv      string `json:"model_env"`
	APIKeyEnv     string `json:"api_key_env"`
	ModelRevision string `json:"model_revision"`
	QueryPrefix   string `json:"query_prefix"`
}
type QdrantSettings struct {
	URL              string `json:"url"`
	URLEnv           string `json:"url_env"`
	APIKeyEnv        string `json:"api_key_env"`
	CollectionPrefix string `json:"collection_prefix"`
}
type ProcessingSettings struct {
	FFmpeg                    string  `json:"ffmpeg"`
	FFprobe                   string  `json:"ffprobe"`
	AllowKeyframeFallback     bool    `json:"allow_keyframe_fallback"`
	VideoFrameChangeThreshold float64 `json:"video_frame_change_threshold"`
	EmbeddingBatchSize        int     `json:"embedding_batch_size"`
	MaxEntryBytes             int64   `json:"max_entry_bytes"`
	MaxExpandedBytes          int64   `json:"max_expanded_bytes"`
	MaxEntries                int     `json:"max_entries"`
	RequestTimeoutSeconds     int     `json:"request_timeout_seconds"`
	ProcessTimeoutSeconds     int     `json:"process_timeout_seconds"`
	ImageMaxDimension         int     `json:"image_max_dimension"`
	MaxImagePixels            int64   `json:"max_image_pixels"`
}

func DefaultSettings() Settings {
	return Settings{
		Embedding:  EmbeddingSettings{BaseURL: "http://127.0.0.1:18182/v1", Model: "embed", ModelRevision: "embeddinggemma-2-q8_0", QueryPrefix: "task: search result | query: "},
		Qdrant:     QdrantSettings{URL: "http://127.0.0.1:6333", CollectionPrefix: "vgalary"},
		Processing: ProcessingSettings{FFmpeg: "ffmpeg", FFprobe: "ffprobe", VideoFrameChangeThreshold: 0.12, EmbeddingBatchSize: 100, MaxEntryBytes: 2 << 30, MaxExpandedBytes: 10 << 30, MaxEntries: 100000, RequestTimeoutSeconds: 180, ProcessTimeoutSeconds: 600, ImageMaxDimension: 1024, MaxImagePixels: 100000000},
	}
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var collectionName = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func envValue(name, fallback string) (string, error) {
	if name == "" {
		return fallback, nil
	}
	if !envName.MatchString(name) {
		return "", fmt.Errorf("invalid environment variable name %q", name)
	}
	value, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("environment variable %s is missing or empty", name)
	}
	return value, nil
}

type resolvedSettings struct {
	Settings
	EmbeddingURL, Model, EmbeddingKey, QdrantURL, QdrantKey string
}

func resolve(s Settings) (resolvedSettings, error) {
	r := resolvedSettings{Settings: s}
	fields := []struct {
		name, fallback string
		dest           *string
	}{
		{s.Embedding.BaseURLEnv, s.Embedding.BaseURL, &r.EmbeddingURL},
		{s.Embedding.ModelEnv, s.Embedding.Model, &r.Model},
		{s.Embedding.APIKeyEnv, "", &r.EmbeddingKey},
		{s.Qdrant.URLEnv, s.Qdrant.URL, &r.QdrantURL},
		{s.Qdrant.APIKeyEnv, "", &r.QdrantKey},
	}
	for _, f := range fields {
		v, err := envValue(f.name, f.fallback)
		if err != nil {
			return r, err
		}
		*f.dest = v
	}
	for _, raw := range []string{r.EmbeddingURL, r.QdrantURL} {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return r, fmt.Errorf("URL must be an http(s) base URL without credentials, query or fragment")
		}
	}
	r.EmbeddingURL = strings.TrimRight(r.EmbeddingURL, "/")
	r.QdrantURL = strings.TrimRight(r.QdrantURL, "/")
	if strings.TrimSpace(r.Model) == "" {
		return r, fmt.Errorf("embedding model is required")
	}
	if !collectionName.MatchString(s.Qdrant.CollectionPrefix) || len(s.Qdrant.CollectionPrefix) > 80 {
		return r, fmt.Errorf("invalid collection prefix")
	}
	p := s.Processing
	if p.EmbeddingBatchSize < 1 || p.EmbeddingBatchSize > 1000 {
		return r, fmt.Errorf("embedding_batch_size must be between 1 and 1000")
	}
	if math.IsNaN(p.VideoFrameChangeThreshold) || math.IsInf(p.VideoFrameChangeThreshold, 0) || p.VideoFrameChangeThreshold < 0 || p.VideoFrameChangeThreshold > 1 {
		return r, fmt.Errorf("video_frame_change_threshold must be between 0 and 1")
	}
	if p.FFmpeg == "" || p.FFprobe == "" || p.MaxEntryBytes < 1 || p.MaxExpandedBytes < p.MaxEntryBytes || p.MaxEntries < 1 || p.RequestTimeoutSeconds < 1 || p.ProcessTimeoutSeconds < 1 || p.ImageMaxDimension < 32 || p.ImageMaxDimension > 4096 || p.MaxImagePixels < 1 {
		return r, fmt.Errorf("invalid processing limits")
	}
	return r, nil
}

// A profile excludes credentials and endpoint location: changing either is not a new vector space.
func (r resolvedSettings) profileID() string {
	fields := []any{"v1", r.Model, r.Embedding.ModelRevision, r.Embedding.QueryPrefix, r.Processing.ImageMaxDimension, r.Processing.AllowKeyframeFallback}
	// Zero retains the legacy profile so disabling the filter restores the old index policy.
	if r.Processing.VideoFrameChangeThreshold > 0 {
		fields = append(fields, "rgb-mad-64-v1", r.Processing.VideoFrameChangeThreshold)
	}
	b, _ := json.Marshal(fields)
	return digest(b)[:24]
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
