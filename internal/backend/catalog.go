package backend

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
)

type catalogFilter struct {
	ProfileID string
	SourceID  string
	MediaType string
	Limit     int
	Offset    int
}

func (s *Store) catalogAssets(ctx context.Context, filter catalogFilter) ([]Asset, error) {
	query := `SELECT a.body FROM records a WHERE a.kind='asset'
 AND EXISTS (SELECT 1 FROM records s WHERE s.kind='source'
   AND s.id=json_extract(CAST(a.body AS TEXT),'$.source_id')
   AND json_extract(CAST(s.body AS TEXT),'$.active')=1)`
	arguments := []any{}
	for _, field := range []struct{ name, value string }{
		{"profile_id", filter.ProfileID}, {"source_id", filter.SourceID}, {"media_type", filter.MediaType},
	} {
		if field.value != "" {
			query += " AND json_extract(CAST(a.body AS TEXT),'$." + field.name + "')=?"
			arguments = append(arguments, field.value)
		}
	}
	query += ` ORDER BY julianday(json_extract(CAST(a.body AS TEXT),'$.updated_at')) DESC,a.id ASC LIMIT ? OFFSET ?`
	arguments = append(arguments, filter.Limit, filter.Offset)
	rows, err := s.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	assets := []Asset{}
	for rows.Next() {
		var body []byte
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var asset Asset
		if err := json.Unmarshal(body, &asset); err != nil {
			return nil, err
		}
		assets = append(assets, asset)
	}
	return assets, rows.Err()
}

func (s *Store) catalogCounts(ctx context.Context, profileID string) (map[string]int, error) {
	query := `SELECT s.id,COUNT(a.id) FROM records s LEFT JOIN records a
 ON a.kind='asset' AND json_extract(CAST(a.body AS TEXT),'$.source_id')=s.id`
	arguments := []any{}
	if profileID != "" {
		query += ` AND json_extract(CAST(a.body AS TEXT),'$.profile_id')=?`
		arguments = append(arguments, profileID)
	}
	query += ` WHERE s.kind='source' AND json_extract(CAST(s.body AS TEXT),'$.active')=1 GROUP BY s.id`
	rows, err := s.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	counts := map[string]int{}
	for rows.Next() {
		var id string
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			return nil, err
		}
		counts[id] = count
	}
	return counts, rows.Err()
}

func (a *App) catalogProfile(r *http.Request) (string, error) {
	id := r.URL.Query().Get("profile_id")
	if id == "" {
		if err := a.store.get(r.Context(), "meta", "active_profile", &id); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}
	return id, nil
}

func (a *App) listAssets(w http.ResponseWriter, r *http.Request) {
	filter := catalogFilter{SourceID: r.URL.Query().Get("source_id"), MediaType: r.URL.Query().Get("media_type"), Limit: 24}
	for name, destination := range map[string]*int{"limit": &filter.Limit, "offset": &filter.Offset} {
		if value := r.URL.Query().Get(name); value != "" {
			number, err := strconv.Atoi(value)
			if err != nil || number < 0 || (name == "limit" && (number < 1 || number > 100)) {
				apiError(w, http.StatusBadRequest, errors.New("invalid catalog limit/offset"))
				return
			}
			*destination = number
		}
	}
	if filter.MediaType != "" && filter.MediaType != "image" && filter.MediaType != "video" {
		apiError(w, http.StatusBadRequest, errors.New("invalid media_type"))
		return
	}
	var err error
	filter.ProfileID, err = a.catalogProfile(r)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	assets, err := a.store.catalogAssets(r.Context(), filter)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, assets)
}

func (a *App) assetCounts(w http.ResponseWriter, r *http.Request) {
	profileID, err := a.catalogProfile(r)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	counts, err := a.store.catalogCounts(r.Context(), profileID)
	if err != nil {
		apiError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, counts)
}
