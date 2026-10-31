export type Page = 'search' | 'sources' | 'jobs' | 'settings';
export type MediaType = '' | 'image' | 'video';
export interface Source {
  id: string;
  root_path: string;
  active: boolean;
  created_at: string;
}
export interface ArchiveEntry {
  name: string;
  index: number;
}
export interface Point {
  id: string;
  timestamp_ms?: number;
  pts?: string;
  time_base?: string;
  selection_kind?: string;
  change_score?: number;
  stream_index: number;
}
export interface Asset {
  id: string;
  source_id: string;
  location: { relative_path: string; archive_entries?: ArchiveEntry[] };
  media_type: 'image' | 'video';
  revision: string;
  profile_id: string;
  collection: string;
  size: number;
  points: Point[];
  updated_at: string;
}
export interface Result {
  asset: Asset;
  score?: number;
  point_id: string;
  timestamp_ms?: number;
  timestamp?: string;
  selection_kind?: string;
  path: string;
  thumbnail_url?: string;
}
export interface Job {
  id: string;
  source_id: string;
  status: 'queued' | 'running' | 'succeeded' | 'failed' | 'canceled';
  created_at: string;
  started_at?: string;
  finished_at?: string;
  discovered: number;
  indexed: number;
  skipped: number;
  embedded: number;
  cycles: number;
  issue_count: number;
  error?: string;
  issues: { location: string; code: string; message: string }[];
}
export interface Settings {
  embedding: {
    base_url: string;
    base_url_env: string;
    model: string;
    model_env: string;
    api_key_env: string;
    model_revision: string;
    query_prefix: string;
  };
  qdrant: { url: string; url_env: string; api_key_env: string; collection_prefix: string };
  processing: {
    ffmpeg: string;
    ffprobe: string;
    allow_keyframe_fallback: boolean;
    video_frame_change_threshold: number;
    max_entry_bytes: number;
    max_expanded_bytes: number;
    max_entries: number;
    job_timeout_seconds: number;
    request_timeout_seconds: number;
    process_timeout_seconds: number;
    image_max_dimension: number;
    max_image_pixels: number;
  };
}
export interface SettingsEnvelope {
  settings: Settings;
  sources: Source[];
  active_profile?: string;
  configuration_error?: string;
  effective?: {
    embedding_url: string;
    model: string;
    qdrant_url: string;
    embedding_auth_configured: boolean;
    qdrant_auth_configured: boolean;
  };
}
export interface Session {
  authenticated: boolean;
  auth_required: boolean;
}
