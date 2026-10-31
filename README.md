# vgalary

Go로 만든 이미지·영상 검색 앱입니다. 폴더와 중첩 ZIP·RAR를 탐색해 llama.cpp의 EmbeddingGemma 2로 임베딩하고, Qdrant에서 자연어로 검색합니다. 웹 UI에서 검색, 영상 시점 이동, 원본 다운로드·내보내기, 디렉토리·작업·연결 설정을 관리합니다.

빌드에는 Git, Node.js 22.13 이상, npm, Go 1.27.3 이상이 필요합니다. 색인에는 `ffmpeg`·`ffprobe`, 멀티모달 임베딩이 켜진 llama.cpp 서버, Qdrant를 사용합니다. Windows ARM64와 FFmpeg 9.0.2에서 검증했습니다.

```powershell
# Qdrant: Docker를 사용하는 경우
docker compose up -d

# Docker를 사용할 수 없으면 위 명령 대신 WSLC 사용
wslc run --detach --name vgalary-qdrant --publish 127.0.0.1:6333:6333 --volume vgalary-qdrant-data:/qdrant/storage qdrant/qdrant:v1.20.0

npm ci --prefix web
npm run build --prefix web
go run ./cmd/vgalary -data-dir .local/app
```

기본 웹 UI·API 주소는 `http://127.0.0.1:8181`입니다. 임베딩 서버는 `http://127.0.0.1:18182/v1`, Model은 `embed`, Qdrant는 `http://127.0.0.1:6333`을 기본으로 사용합니다. 연결 설정과 인덱싱 디렉토리는 SQLite에 저장하고 웹 UI나 API로 변경합니다. `-data-dir`를 생략하면 OS 사용자 설정 디렉토리 아래 `vgalary`를 사용합니다. 같은 데이터 디렉토리는 한 앱 프로세스에서 사용하세요.

소스에서 한 번에 빌드하고 로컬 서버를 시작하려면 다음 스크립트를 사용합니다. 웹 빌드 결과를 Go 실행 파일에 포함합니다.

```powershell
node scripts/deploy.mjs
```

`vgalary.saturday.ne.kr`은 Cloudflare Pages에서 **설치 안내와 설치 스크립트만** 배포하는 사이트입니다. Windows에서는 `install.ps1`, macOS·Linux에서는 `install.sh`를 받아 사용자 디렉토리에 설치합니다. 두 설치기는 OS 판별 없이 Git 복제·빌드·설치·업데이트를 처리합니다. 앱은 각 이용자의 컴퓨터에서 실행하며, 외부 공개 여부와 접속 방식은 이용자가 선택합니다. Pages 빌드 설정, 설치 위치와 선택적 도메인 모드는 [설치·배포 문서](docs/deployment.md)를 참고하세요.

```powershell
$base = 'http://127.0.0.1:8181'
$jsonType = 'application/json; charset=utf-8'

# 서버에서 접근할 수 있는 디렉토리 등록
$source = Invoke-RestMethod "$base/api/sources" -Method Post -ContentType $jsonType `
    -Body (@{ root_path = 'D:/media' } | ConvertTo-Json)

# 색인 시작, 반환된 job ID로 진행 상황 조회
$job = Invoke-RestMethod "$base/api/sources/$($source.id)/scan" -Method Post `
    -ContentType $jsonType -Body '{"force":false}'
Invoke-RestMethod "$base/api/jobs/$($job.id)"

# 작업이 succeeded가 된 뒤 자연어 검색
Invoke-RestMethod "$base/api/search" -Method Post -ContentType $jsonType `
    -Body (@{ query = '손에 들고 있는 파란 고양이 장난감'; limit = 10 } | ConvertTo-Json)

# 등록 제거: 수집 중단, 검색 제외, 색인 정리. 원본 파일은 보존
Invoke-RestMethod "$base/api/sources/$($source.id)" -Method Delete
```

영상은 프레임별로 임베딩하고, 검색 결과는 영상 단위로 묶어 가장 잘 일치한 프레임의 `timestamp_ms`·표시용 타임스탬프를 반환합니다. H.264/H.265는 패킷의 NAL을 검사해 IDR을 선택합니다. AV1·VP9처럼 IDR 개념이 없는 코덱은 `processing.allow_keyframe_fallback`을 명시적으로 켜면 키프레임 기준으로 처리하며 `selection_kind`로 구분합니다. 기본값은 꺼짐입니다.

영상의 첫 IDR·키프레임은 유지하고, 이후에는 **마지막으로 선택한 프레임과 화면이 충분히 달라졌을 때만** 임베딩합니다. 추출 이미지를 64×64로 축소해 정규화한 평균 RGB 차이를 계산하며 `processing.video_frame_change_threshold`의 기본값은 `0.12`입니다. 값이 높을수록 더 큰 변화를 요구하고, `0`이면 필터를 끄고 모든 후보를 처리합니다. 임베딩 전 비교이므로 건너뛴 프레임에는 모델을 호출하지 않습니다. 선택한 프레임의 원래 시점을 보존하며, 변경 점수는 상세 포인트의 `change_score`에 기록합니다. 설정 변경 후 소스를 다시 스캔하면 새 정책으로 색인합니다.

폴더와 중첩 압축에는 고정 깊이 제한이 없습니다. 디렉토리 링크의 조상 순환과 중첩 압축의 조상 해시 반복을 감지합니다. 자원 한도나 읽기 오류로 스캔이 불완전하면 발견하지 못한 기존 자료를 일괄 삭제하지 않습니다. 압축 내부 항목은 경로와 영상 시점만으로 표시할 수 있고, 미리보기를 위해 추가 추출하지 않습니다. 명시적인 다운로드·내보내기는 중첩 항목을 추출합니다.

설정의 API Key에는 비밀 값이 아닌 **환경 변수 이름만** 입력합니다. 참조를 비워 두면 인증 없이 연결합니다. URL·Model은 앱 저장값과 선택적 환경 변수 참조를 지원합니다. 참조한 변수가 비어 있으면 오류로 처리합니다. 프로세스 환경 값 변경은 재시작이 필요하며, API로 저장한 설정 변경은 다음 작업부터 적용됩니다.

검증은 다음 명령으로 실행합니다.

```powershell
go mod tidy
node --test scripts/install.test.mjs
npm run lint --prefix web
npm test --prefix web
npm run build --prefix web
go build ./...
go vet ./...
go test ./...
golangci-lint run --no-config ./...

# 주어진 로컬 원본을 복사해 테스트 데이터 생성. 기존 출력 디렉토리는 덮어쓰지 않음
./scripts/make-local-fixtures.ps1 -ImageDirectory 'G:/내 드라이브' `
    -VideoPath 'G:/내 드라이브/video/정말 그리웠어요.webm'

# 실제 llama.cpp·Qdrant를 사용하는 통합 테스트
$env:VGALARY_LIVE_TEST = '1'
$env:VGALARY_FIXTURE_ROOT = (Resolve-Path 'testdata/local/media').Path
go test ./internal/backend -run '^TestLiveBackend$' -v -count=1 -timeout 25m
```

실제 통합 테스트는 별도 앱 데이터·컬렉션을 사용하고 테스트 컬렉션을 정리합니다. `VGALARY_TEST_DATA_DIR`를 지정한 경우에는 그 경로에 설정과 색인 상태, `verification.json`을 보존합니다. 일반 `go test`는 외부 서비스 테스트를 건너뛰며, FFmpeg가 있으면 짧은 H.264/H.265 검출 테스트를 실행합니다. 로컬 테스트 미디어·DB·캐시는 Git에 포함하지 않습니다.

지원 이미지 후보는 JPEG·PNG·GIF·WebP이며 GIF는 첫 프레임을 사용합니다. 영상 후보는 MP4·MKV·WebM·MOV·AVI·M4V·TS이고 실제 디코딩은 설치한 FFmpeg에 따릅니다. 압축 내부 링크의 가상 경로 해석, 암호화·분할 압축, OCR·음성 검색, 원본 영상의 재생 호환성을 위한 변환은 아직 제공하지 않습니다. 전체 캐시의 자동 용량 관리와 비정상 종료로 남은 임시 파일 정리는 후속 작업입니다.

[API와 설정](docs/api.md) · [배포와 웹 개발](docs/deployment.md) · [백엔드 검증 기록](docs/backend-verification.md) · [프로젝트 blueprint](docs/blueprint.md)
