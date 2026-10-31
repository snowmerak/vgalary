# 백엔드 API와 설정

상태: 현재 구현된 HTTP API. 기준은 `internal/backend/http.go`와 설정 타입 `internal/backend/config.go`다. 갱신일: 2026-10-31.

기본 주소는 `http://127.0.0.1:8181`이다. JSON 요청에는 `Content-Type: application/json; charset=utf-8`을 사용한다. 로컬 기본 모드는 loopback Host만 허용하고 다른 Origin의 변경 요청을 거절한다. 웹 UI도 같은 서버에서 제공한다.

선택적 `-public-origin https://gallery.example.com` 모드는 이용자의 HTTPS 프록시 뒤에서 단일 사용자 비밀번호 인증을 적용한다. 비밀번호는 `-web-password-env`가 지정한 환경 변수(기본 `VGALARY_WEB_PASSWORD`, 최소 16바이트)에서 읽으며 API에 값을 반환하지 않는다. 로그인·세션 조회·health를 제외한 API는 인증 쿠키가 없으면 `401`을 반환한다. 배포 구성은 [배포 문서](deployment.md)를 따른다. `vgalary.saturday.ne.kr`은 앱 주소가 아닌 설치 파일 배포 사이트다.

| 메서드 | 경로 | 동작 |
| --- | --- | --- |
| GET | `/api/health` | 프로세스 상태. 외부 서비스 준비 여부와 별개 |
| GET | `/api/session` | `authenticated`, `auth_required` 확인 |
| POST | `/api/login` | `{"password":"..."}`로 로그인. HttpOnly·SameSite=Strict 세션 쿠키, 공개 모드는 Secure |
| POST | `/api/logout` | 현재 브라우저의 세션 쿠키 제거 |
| GET / PUT | `/api/settings` | 설정 조회 / 전체 설정 교체 |
| GET | `/api/models` | 모델 ID 목록. 서버 프리셋·명령줄은 반환하지 않음 |
| POST | `/api/connections/test` | 모델 목록, 텍스트·테스트 이미지 두 장, Qdrant 연결 확인 |
| GET | `/api/directories` | 서버 폴더 선택기. 선택적 `path`, `offset`, `limit`로 한 단계 하위 폴더 조회 |
| GET / POST | `/api/sources` | 활성 인덱싱 디렉토리 목록 / 등록 |
| DELETE | `/api/sources/{id}` | 등록 제거, 작업 취소, 검색 제외, 원본 보존 |
| POST | `/api/sources/{id}/scan` | `{"force":false}`로 비동기 색인 시작 |
| GET | `/api/jobs`, `/api/jobs/{id}` | 작업 목록 / 진행 상황과 파일별 오류 |
| POST | `/api/jobs/{id}/cancel` | 작업 취소 |
| POST | `/api/jobs/{id}/retry` | 현재 설정으로 새 작업 생성 |
| POST | `/api/search` | 자연어 검색 |
| GET | `/api/assets` | 활성 검색 프로필·소스의 카탈로그 페이지. `limit`, `offset`, `source_id`, `media_type`, `profile_id` |
| GET | `/api/assets/counts` | 활성 소스별 전체 미디어 개수. 선택적 `profile_id` |
| GET | `/api/assets/{id}` | 원본 위치·리비전·프레임별 포인트 |
| GET | `/api/assets/{id}/thumbnail` | 기존 썸네일. 선택적 `point_id` |
| GET | `/api/assets/{id}/content` | 일반 파일 콘텐츠·영상 Range 응답 |
| GET | `/api/assets/{id}/download` | 원본 다운로드. 중첩 압축 항목도 추출 |
| POST | `/api/assets/{id}/export` | `{"directory":"D:/exports"}`로 서버 측 복사 |
| GET | `/api/cleanup` | 남은 Qdrant 포인트 정리 작업·오류 |

성공 시 등록·내보내기는 201, 작업 시작·취소는 202, 소스 제거는 204를 반환한다. 오류 응답은 `{"error":"설명"}`이다. 없는 항목은 404, 잘못된 설정·본문은 400, 활성 작업 충돌과 원본 변경·압축 미리보기 불가는 409로 구분한다. 현재 검색 실패와 외부 연결 실패는 502로 반환한다. 검색 전에 성공한 색인이 필요하다.

## 폴더 선택

`GET /api/directories`는 `path`, `parent`, `home`, `roots`, `entries: [{name, path}]`, `total`, `offset`, `limit`를 반환한다. 경로를 생략하면 서버 사용자 홈을 열고, 상대 경로는 서버 작업 디렉토리 기준으로 해석한다. 결과 경로는 심볼릭 링크·junction을 해석한 절대 경로이며 루트의 `parent`는 빈 문자열이다. Windows의 빠른 위치에는 홈과 논리 드라이브, macOS·Linux에는 홈과 `/`가 포함된다. 파일·압축 내부 항목은 표시하지 않으며 재귀 탐색하지 않는다. 폴더 이름은 대소문자를 구분하지 않는 이름순으로 정렬하고 페이지 기본 크기는 200, 최대 크기는 500이다. 없는 경로는 404, 접근 권한 부족은 403, 파일 경로·잘못된 페이지 값은 400이다. 도메인 모드에서는 다른 API와 동일한 인증이 필요하다. 이 API는 폴더를 등록하거나 색인을 시작하지 않는다.

## 설정

`GET /api/settings`는 `settings`, `sources`, 비밀 값이 제외된 `effective`, 존재한다면 `active_profile`을 반환한다. 잘못된 환경 참조가 있으면 `configuration_error`를 포함한다. `PUT`에는 응답 전체가 아니라 **`settings` 객체만** 보낸다. 부분 수정은 아직 지원하지 않는다. 디렉토리 목록은 `/api/sources`로 관리하며 같은 SQLite에 저장한다.

```powershell
$base = 'http://127.0.0.1:8181'
$saved = Invoke-RestMethod "$base/api/settings"
$settings = $saved.settings
$settings.processing.allow_keyframe_fallback = $true  # AV1/VP9 등
$settings.processing.video_frame_change_threshold = 0.12 # 마지막 선택 프레임과의 화면 차이
$settings.embedding.api_key_env = ''                  # 인증 없음
Invoke-RestMethod "$base/api/settings" -Method Put `
    -ContentType 'application/json; charset=utf-8' `
    -Body ($settings | ConvertTo-Json -Depth 8)
```

| 필드 | 기본값 / 의미 |
| --- | --- |
| `embedding.base_url` | `http://127.0.0.1:18182/v1`. `/embeddings`, `/models`를 아래에 연결 |
| `embedding.model` | `embed` |
| `embedding.base_url_env`, `embedding.model_env` | 빈 문자열. 지정하면 저장된 URL·Model 대신 해당 환경 변수 사용 |
| `embedding.api_key_env` | 빈 문자열. 지정하면 해당 환경 값을 Bearer 인증에 사용 |
| `embedding.model_revision` | `embeddinggemma-2-q8_0`. 같은 별칭의 가중치를 교체하면 변경 필요 |
| `embedding.query_prefix` | `task: search result \| query: ` |
| `qdrant.url` | `http://127.0.0.1:6333`. REST 연결 |
| `qdrant.url_env` | 빈 문자열. 지정하면 해당 환경의 URL 사용 |
| `qdrant.api_key_env` | 빈 문자열. 지정하면 해당 환경 값을 `api-key` 헤더에 사용 |
| `qdrant.collection_prefix` | `vgalary`. 앱 소유 프로필별 컬렉션 이름의 접두사 |
| `processing.ffmpeg`, `processing.ffprobe` | 실행 파일 이름 또는 경로 |
| `processing.allow_keyframe_fallback` | `false`. IDR 개념이 없는 코덱에 키프레임 대체 허용 |
| `processing.video_frame_change_threshold` | `0.12`. 정규화 평균 RGB 차이의 선택 임계값(0~1). `0`은 필터 비활성화 |
| `processing.max_entry_bytes` | 2 GiB. 단일 원본·압축·해제 항목 상한 |
| `processing.max_expanded_bytes` | 10 GiB. 스캔 또는 다운로드의 누적 압축 해제량 상한 |
| `processing.max_entries` | 100,000. 탐색 작업량·영상 키프레임 개수 상한. 깊이 상한은 없음 |
| `processing.request_timeout_seconds` | 외부 HTTP 요청당 180초 |
| `processing.process_timeout_seconds` | FFmpeg/ffprobe 실행당 600초 |
| `processing.image_max_dimension` | 1,024. 임베딩 전 이미지의 최대 변 길이 |
| `processing.max_image_pixels` | 100,000,000. 이미지 디코딩 픽셀 상한 |

환경 변수 참조를 지정했는데 값이 없거나 비어 있으면 오류다. 비밀 값의 직접 입력 필드는 없다. 설정 변경은 저장 직후 새 작업에 적용하고, 진행 중인 작업은 시작 시의 설정 스냅샷을 유지한다. 프로세스 환경의 값 변경은 재시작이 필요하다.

전체 디렉토리 색인 작업에는 시간 제한을 적용하지 않는다. 이전 버전에서 저장한 `processing.job_timeout_seconds`는 현재 설정과 대기·재개 작업 모두에서 무시하며, API 응답이나 새 저장 값에는 포함하지 않는다. 개별 외부 HTTP 요청과 FFmpeg/ffprobe 실행에는 위 제한을 유지한다. 수동 취소·소스 제거·앱 종료로 작업을 중단할 수 있다. 이 동작은 새 버전의 서버를 실행한 이후 적용된다.

기존 저장 설정에 `video_frame_change_threshold`가 없으면 앱 시작 시 기본값을 채운다. 명시적으로 저장한 `0`은 그대로 유지한다. 임계값과 비교 알고리즘 버전은 색인 프로필에 포함하므로 변경 후 소스를 스캔해야 검색 포인트가 바뀐다. 이전 프로필의 벡터를 새 정책의 완료 상태로 잘못 재사용하지 않는다. `0`은 기존 전체 프레임 프로필과 호환된다.

벡터 공간은 Model·모델 리비전·질의 프롬프트·이미지 전처리·프레임 정책으로 식별한다. Qdrant URL·컬렉션 접두사는 별도 색인 대상 식별에 포함해 다른 저장소의 완료 상태를 잘못 재사용하지 않는다. 검색은 활성 프로필의 설정을 사용한다. 모델이나 Qdrant 대상을 바꾸면 등록된 모든 디렉토리를 새 설정으로 스캔한 뒤 검색 프로필을 전환한다. 이전 프로필의 카탈로그·컬렉션은 자동으로 지우지 않는다.

## 디렉토리와 작업

등록 요청은 `{"root_path":"D:/media"}`다. 경로는 앱 서버 기준이며, 앱 데이터 디렉토리와 겹칠 수 없다. 같은 실제 디렉토리의 중복 등록을 거절한다. 상하위 루트는 별개 소스로 등록할 수 있고, 제거는 선택한 소스에만 적용한다.

작업은 `queued → running → succeeded / failed / canceled`로 전이한다. 작업 ID, 발견·색인·재사용 개수, 이미지 임베딩 호출 수 `embedded`, 순환 수 `cycles`, 파일별 `issues`를 조회할 수 있다. `embedded`에는 연결/차원 확인을 위한 텍스트 요청은 포함하지 않는다. 오류 상세는 최대 100개와 전체 오류 수를 저장한다. `force:true`는 같은 내용도 다시 임베딩한다.

원본 내용 해시·프로필이 같으면 기존 Qdrant 포인트 존재를 확인해 임베딩을 생략한다. 다른 위치에 있는 동일 미디어도 벡터를 재사용하되 위치와 포인트 ID는 별도로 유지한다. 최초 성공 스캔 후 새 소스를 추가한 경우에는 같은 프로필로 바로 검색에 합류한다.

스캔이 완전히 성공한 범위에서만 사라진 파일을 제거한다. 작업 중 취소·해제 오류·자원 한도 초과는 기존 미발견 항목 삭제로 이어지지 않는다. 소스 제거 시에는 먼저 비활성화하여 검색과 새 작업에서 제외하고, 실행 중 작업의 결과 활성화도 막는다. Qdrant 정리 실패는 영속 작업으로 남겨 30초 주기로 재시도한다. 앱 종료 중 중단된 실행 작업은 재시작 후 재개한다.

## 카탈로그 페이지

`GET /api/assets?limit=24&offset=0`은 기존과 같은 Asset 배열을 반환한다. 기본 `limit`은 24이며 1~100을 허용한다. `offset`은 0 이상의 정수다. `media_type`은 생략하거나 `image`, `video`를 사용하고, `source_id`로 디렉토리를 제한할 수 있다. `profile_id`를 생략하면 활성 검색 프로필을 사용하며, 아직 활성 프로필이 없으면 모든 프로필을 조회한다. 제거된 소스는 제외한다. 필터를 적용한 뒤 최근 갱신 시각 내림차순, 동일 시각에는 Asset ID 오름차순으로 SQLite에서 페이지를 조회한다. 잘못된 페이지 값·미디어 종류는 400, 마지막 페이지 이후는 빈 배열이다.

웹 UI는 한 번에 25개를 요청하여 24개를 표시하고, 남은 한 개로 다음 페이지 유무를 판단한다. `더 보기`는 표시한 개수를 `offset`으로 요청한다. 검색어·필터·프로필 또는 색인 상태가 바뀌면 첫 페이지에서 다시 시작한다. 페이지 사이에 카탈로그가 변경되면 위치가 이동할 수 있으며, 고정 스냅샷을 보장하지는 않는다.

`GET /api/assets/counts`는 같은 프로필 기준의 `{"소스 ID": 개수}` 객체를 반환한다. 활성 소스에는 0개도 포함하고 제거된 소스는 제외한다. 디렉토리 화면은 이 집계를 사용하므로 페이지 크기와 관계없이 전체 개수를 표시한다. 두 API 모두 도메인 모드에서는 인증이 필요하다.

## 검색과 파일 가져오기

```json
{
  "query": "금색 캐릭터 배지",
  "limit": 20,
  "offset": 0,
  "source_id": "선택 사항",
  "media_type": "image"
}
```

`media_type`은 생략하거나 `image`, `video`를 사용한다. `limit`은 1~100, `offset`은 0~10,000이다. 응답의 `results`에는 `asset`, `score`, `point_id`, 표시용 전체 `path`, 있을 경우 `thumbnail_url`이 들어간다. 영상에는 `timestamp_ms`, `timestamp`(`HH:MM:SS.mmm`), `selection_kind`도 포함한다. 점수는 Cosine 유사도이며 정확도 확률이 아니다.

영상 한 개의 여러 포인트가 검색되면 최고 점수의 프레임을 대표로 반환한다. 모든 선택 프레임의 원본 PTS·time base·스트림 번호는 미디어 상세의 `points`에 보존한다. H.264/H.265의 `selection_kind`는 `idr`, 명시적으로 허용한 다른 코덱은 `random_access_keyframe`이다. 이미지에는 영상 타임스탬프 필드를 넣지 않는다. 검색은 오래된 포인트를 카탈로그와 대조해 제거한 뒤 최대 10,000개 후보 안에서 결과를 채운다.

영상은 FFmpeg로 후보 IDR·키프레임을 추출한 뒤, 임베딩 전에 64×64 RGB 픽셀의 평균 절대 차이를 채널 최대값으로 정규화해 비교한다. 의미적 유사도나 변경된 픽셀 면적의 비율이 아니라 화면 차이 점수다. 첫 후보는 항상 유지하고, 나머지는 마지막으로 **채택한** 프레임과의 점수가 임계값 이상인 경우에만 채택한다. 건너뛴 후보를 비교 기준으로 삼지 않으므로 점진적인 변화가 누적되면 선택된다. 선택 시점은 추정 구간이 아닌 해당 원본 프레임의 시점이다.

필터를 사용한 영상의 두 번째 이후 포인트에는 `change_score`를 저장한다. 첫 프레임은 비교 대상이 없어 이 필드가 없으며, 이미지와 필터를 끈 영상에도 부여하지 않는다. 화면이 고정된 영상은 첫 포인트 하나만 남을 수 있다. `/api/assets`는 새 정책이 활성화되면 이전 프로필을 기본 목록에서 제외하며, 과거 컬렉션은 재색인 완료 후에도 자동으로 삭제하지 않는다.

중첩 압축의 위치는 `relative_path`와 순서 있는 `archive_entries`(`name`, `index`)로 표현한다. `!/`로 이어진 `path`는 표시용이다. 다운로드는 이 항목 체인을 따라 원본을 추출하며 경로 문자열을 재파싱하지 않는다. 미리보기 캐시가 없으면 썸네일은 404, 압축 항목의 인라인 콘텐츠는 409를 반환하고 자동으로 다시 추출하지 않는다.

내보내기 대상은 이미 존재하는 서버 측 디렉토리여야 하며 앱 데이터·수집 루트와 겹칠 수 없다. 원본 이름에 UUID를 붙인 새 파일을 만들어 덮어쓰기를 방지한다. 다운로드·내보내기 전에 원본 바깥 파일의 크기·수정 시각을 확인해 변경된 압축이나 파일의 오래된 위치로 잘못 접근하지 않게 한다.
