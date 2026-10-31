package backend

import "time"

type Source struct {
	ID        string    `json:"id"`
	RootPath  string    `json:"root_path"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}
type ArchiveEntry struct {
	Name  string `json:"name"`
	Index int    `json:"index"`
}
type Location struct {
	RelativePath   string         `json:"relative_path"`
	ArchiveEntries []ArchiveEntry `json:"archive_entries,omitempty"`
}
type Asset struct {
	ID           string     `json:"id"`
	SourceID     string     `json:"source_id"`
	Location     Location   `json:"location"`
	MediaType    string     `json:"media_type"`
	Revision     string     `json:"revision"`
	ProfileID    string     `json:"profile_id"`
	Collection   string     `json:"collection"`
	Size         int64      `json:"size"`
	OuterSize    int64      `json:"outer_size"`
	OuterModTime int64      `json:"outer_mod_time"`
	Points       []PointRef `json:"points"`
	SeenJob      string     `json:"seen_job"`
	UpdatedAt    time.Time  `json:"updated_at"`
}
type PointRef struct {
	ID            string   `json:"id"`
	TimestampMS   *int64   `json:"timestamp_ms,omitempty"`
	PTS           string   `json:"pts,omitempty"`
	TimeBase      string   `json:"time_base,omitempty"`
	SelectionKind string   `json:"selection_kind,omitempty"`
	ChangeScore   *float64 `json:"change_score,omitempty"`
	StreamIndex   int      `json:"stream_index"`
	Thumbnail     string   `json:"-"`
}
type Profile struct {
	ID         string   `json:"id"`
	Collection string   `json:"collection"`
	Dimensions int      `json:"dimensions"`
	Settings   Settings `json:"settings"`
	Model      string   `json:"model"`
}
type JobIssue struct {
	Location string `json:"location"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}
type Job struct {
	ID         string     `json:"id"`
	SourceID   string     `json:"source_id"`
	Status     string     `json:"status"`
	Force      bool       `json:"force"`
	Settings   Settings   `json:"settings"`
	ProfileID  string     `json:"profile_id,omitempty"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	Discovered int        `json:"discovered"`
	Indexed    int        `json:"indexed"`
	Skipped    int        `json:"skipped"`
	Embedded   int        `json:"embedded"`
	Cycles     int        `json:"cycles"`
	IssueCount int        `json:"issue_count"`
	Issues     []JobIssue `json:"issues"`
	Error      string     `json:"error,omitempty"`
}
type SearchRequest struct {
	Query     string `json:"query"`
	Limit     int    `json:"limit"`
	Offset    int    `json:"offset"`
	SourceID  string `json:"source_id,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}
type SearchResult struct {
	Asset         Asset   `json:"asset"`
	Score         float64 `json:"score"`
	PointID       string  `json:"point_id"`
	TimestampMS   *int64  `json:"timestamp_ms,omitempty"`
	Timestamp     string  `json:"timestamp,omitempty"`
	SelectionKind string  `json:"selection_kind,omitempty"`
	Path          string  `json:"path"`
	ThumbnailURL  string  `json:"thumbnail_url,omitempty"`
}
