import { describe, expect, it } from 'vitest';
import { displayPath, filename, libraryResult, timestamp } from './utils';
import type { Asset, Source } from './types';

const asset: Asset = {
  id: 'media',
  source_id: 'source',
  location: {
    relative_path: 'outer.zip',
    archive_entries: [
      { name: 'nested/inside.rar', index: 0 },
      { name: '사진/고양이.jpg', index: 2 },
    ],
  },
  media_type: 'image',
  revision: 'sha',
  profile_id: 'profile',
  collection: 'collection',
  size: 100,
  points: [{ id: 'point', stream_index: 0 }],
  updated_at: '2026-10-31T00:00:00Z',
};
const source: Source = { id: 'source', root_path: 'D:/media/', active: true, created_at: '' };
describe('media location and playback presentation', () => {
  it('keeps the full nested archive chain and the leaf filename', () => {
    expect(filename(asset)).toBe('고양이.jpg');
    expect(displayPath(asset, [source])).toBe('D:/media/outer.zip!/nested/inside.rar!/사진/고양이.jpg');
  });
  it('preserves milliseconds and hours in frame timestamps', () => {
    expect(timestamp(84400)).toBe('00:01:24.400');
    expect(timestamp(3723123)).toBe('01:02:03.123');
    expect(timestamp(0)).toBe('00:00:00.000');
  });
  it('uses actual point metadata for library previews without changing an image to a video timestamp', () => {
    const result = libraryResult(asset, [source]);
    expect(result.point_id).toBe('point');
    expect(result.timestamp_ms).toBeUndefined();
    expect(result.thumbnail_url).toContain('/api/assets/media/thumbnail?point_id=point');
  });
});
