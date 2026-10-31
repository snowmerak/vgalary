# 설치 스크립트 배포와 로컬 실행

갱신일: 2026-10-31. `vgalary.saturday.ne.kr`은 Cloudflare Pages에서 설치 안내와 설치 스크립트만 제공한다. 앱·SQLite·미디어·임베딩 서버·Qdrant는 각 이용자의 컴퓨터에서 실행한다. 앱을 외부에 공개할지, 어떤 프록시나 터널을 사용할지는 이용자가 결정한다.

## 설치 파일을 Cloudflare Pages에 게시

Pages의 Git 연동 프로젝트에서 아래 값을 사용한다.

| 항목 | 값 |
| --- | --- |
| 저장소 루트 디렉토리 | 저장소 루트 `/` |
| 프레임워크 프리셋 | None |
| 빌드 명령 | `node scripts/build-distribution.mjs` |
| 빌드 출력 디렉토리 | `.deploy/pages` |
| 커스텀 도메인 | `vgalary.saturday.ne.kr` |
| 빌드 환경 변수 | `SKIP_DEPENDENCY_INSTALL=1`, `NODE_VERSION=22.23.4` |

빌드는 Node 표준 라이브러리로 정적 파일만 복사한다. 빌드 환경 변수는 Pages 설정에 지정하며, 자동 의존성 설치를 생략해 앱의 Go·npm 의존성을 CI에서 설치하지 않는다. npm 설치, Go 빌드, Pages Functions, API 서버 또는 Cloudflare Tunnel이 필요하지 않다. `wrangler.toml`에도 출력 디렉토리를 선언한다. Git에 변경 사항을 게시한 뒤 Pages에서 저장소와 도메인을 연결한다. 이 저장소의 스크립트는 Cloudflare 계정이나 DNS를 자동 변경하지 않는다.

로컬에서 게시할 파일을 준비하려면:

```text
node scripts/build-distribution.mjs
```

출력에는 `index.html`, `styles.css`, `404.html`, `_headers`, `robots.txt`, `install.ps1`, `install.sh`와 미리보기 이미지 `assets/main-demo.webp`를 포함한다. 미리보기는 임시 데이터로 촬영한 메인 화면을 너비 960px로 축소한 이미지다. Go 실행 파일, DB, 사용자 미디어와 비밀 환경 변수는 포함하지 않는다. 설치기 원본은 `scripts/install.ps1`, `scripts/install.sh`이며 게시용 사본은 빌드 때 생성한다. Pages 사이트는 앱의 `/api` 요청을 중계하거나 로컬 미디어를 받지 않는다.

Pages의 [빌드 명령·출력 디렉토리](https://developers.cloudflare.com/pages/configuration/build-configuration/), [정적 응답 헤더](https://developers.cloudflare.com/pages/configuration/headers/), [커스텀 도메인](https://developers.cloudflare.com/pages/configuration/custom-domains/)은 Cloudflare 공식 문서를 따른다.

## 이용자 설치

Git, Node.js 22.13 이상, npm, Go 1.27.3 이상이 설치되어 있어야 한다. Windows는 PowerShell 설치기를, macOS·Linux는 POSIX `sh` 설치기를 사용한다. OS·아키텍처를 감지하거나 분기하지 않으며 각 스크립트가 직접 Git 복제·설치·업데이트를 수행한다. 웹·Go 빌드는 공통 `scripts/deploy.mjs --build-only`를 호출한다. 사이트에서 파일을 받거나 아래 명령으로 다운로드한다.

Windows PowerShell:

```powershell
Invoke-WebRequest https://vgalary.saturday.ne.kr/install.ps1 -OutFile install.ps1
.\install.ps1
```

macOS·Linux:

```sh
curl -fLo install.sh https://vgalary.saturday.ne.kr/install.sh
sh install.sh
```

설치기는 `https://github.com/snowmerak/vgalary.git`을 `<설치 위치>/source`로 복제하고 `npm ci` → 웹 lint·테스트·타입 검사·빌드 → Go 빌드를 실행한다. 실행 파일은 `<설치 위치>/bin/vgalary`(Windows에서는 `vgalary.exe`)에 둔다. 웹 UI를 실행 파일에 포함하므로 앱 실행 시 별도의 Node 서버가 필요하지 않다.

| OS | 기본 설치 위치 |
| --- | --- |
| PowerShell 설치기 | `%LOCALAPPDATA%/vgalary`, 미설정 시 `~/.local/share/vgalary` |
| sh 설치기 (macOS·Linux) | `$XDG_DATA_HOME/vgalary`, 미설정 시 `~/.local/share/vgalary` |

설치기는 관리자 권한, PATH 변경, 서비스 등록을 요구하지 않는다. 서버를 자동 시작하거나 외부에 공개하지 않으며 설치 후 시작 명령을 출력한다. 출력한 명령으로 실행 파일을 시작하고 `http://127.0.0.1:8181`을 연다. 같은 앱 데이터 디렉토리는 한 프로세스에서만 사용한다.

설치 위치 지정과 업데이트:

```powershell
.\install.ps1 -Directory "원하는 설치 경로"
.\install.ps1 -Directory "원하는 설치 경로" -Update
```

```sh
sh install.sh --directory "원하는 설치 경로"
sh install.sh --directory "원하는 설치 경로" --update
```

업데이트 전에 실행 중인 앱을 종료하고 처음과 같은 스크립트·설치 경로를 사용한다. `-Update` 또는 `--update`는 기존 설치의 깨끗한 Git 체크아웃을 `git pull --ff-only`로 갱신한다. 같은 설치기를 다시 실행하면 기본적으로 현재 체크아웃을 재빌드하며 소스를 자동 갱신하지 않는다. 로컬 수정이나 다른 저장소의 체크아웃을 발견하면 덮어쓰지 않고 중단한다. 실행 파일은 빌드 성공 후 교체한다. 사용자 미디어·SQLite는 설치·업데이트 과정에서 변경하지 않는다. 기존 Node 설치기로 별도 경로에 설치했다면 그 경로를 명시해 이어서 사용한다.

## 소스 체크아웃에서 실행

```text
git clone https://github.com/snowmerak/vgalary.git
cd vgalary
node scripts/deploy.mjs
```

기본 실행은 비밀번호·도메인 설정 없이 `127.0.0.1:8181`에만 바인딩한다. `--build-only`는 `.deploy/`에 실행 파일만 만든다. `--data-dir PATH`로 데이터 위치를 지정할 수 있다. `--update`는 빌드 전에 깨끗한 작업 트리를 확인하고 `git pull --ff-only`를 실행한다. 기존 서버가 있으면 먼저 정상 종료하거나 `--listen 127.0.0.1:다른포트`를 지정한다.

색인에는 별도로 접근 가능한 llama.cpp·Qdrant와 `ffmpeg`·`ffprobe`가 필요하다. 설치기는 이 외부 서비스를 자동 설치하지 않는다. 앱의 설정 화면에서 임베딩 URL·Model·선택적 API Key 환경 변수 이름과 Qdrant 연결을 설정한다.

## SQLite와 앱 데이터

설치한 실행 파일과 앱 데이터의 위치는 별도다. `-data-dir`를 생략하면 다음 정책을 사용한다.

| OS | SQLite 기본 경로 |
| --- | --- |
| Windows | `%APPDATA%/vgalary/catalog.db` |
| macOS | `~/Library/Application Support/vgalary/catalog.db` |
| Linux | `$XDG_CONFIG_HOME/vgalary/catalog.db`, 미설정 시 `~/.config/vgalary/catalog.db` |

설정, 색인 상태와 썸네일은 앱 데이터 디렉토리에 저장한다. 서버 운영 시 서비스 계정이 달라지면 기본 경로도 달라지므로 필요하면 영속 경로를 명시한다. 설치 디렉토리를 정리하기 전에 사용자 지정 `-data-dir`가 그 안에 있는지 확인한다.

## 진단 로그

앱 실행 파일은 기본 `info` 레벨의 JSON 로그를 stderr에 출력한다. 아래 `vgalary`는 설치기가 출력한 실행 파일 경로로 바꿔 사용하며, PATH에 등록했다면 그대로 실행할 수 있다.

```sh
vgalary -log-level info -log-format json 2> vgalary.log
```

기본 출력에는 작업 등록·실행·재개·취소·완료, 실행 중 최대 30초 간격의 진행 관찰, 파일 오류, 의존성 재시도와 정리 장애가 포함된다. 정상 파일 처리·개별 호출·정상 HTTP 요청은 `debug`에서 확인한다. 터미널에서 읽으려면 `-log-format text`를 사용한다. `-log-level`은 `debug`, `info`, `warn`, `error`를 지원하며 옵션 변경은 재시작 후 반영한다.

```sh
vgalary -log-level debug -log-format text
```

`job_id`는 저장된 작업, `run_id`는 이번 실행을 식별한다. 재시작으로 작업이 재개되면 작업 ID는 유지되고 실행 ID는 바뀐다. HTTP 응답의 `X-Request-ID`로 요청 로그를 연결한다. `job.progress`의 단계·단계 경과 시간·마지막 처리 진전 이후 시간을 함께 보며, 주기적 관찰만으로 실제 파일 처리가 진전됐다고 판단하지 않는다.

기본 로그에는 원본·데이터 디렉토리 경로나 검색어·API Key·본문을 남기지 않는다. 필요한 경우에만 `-log-paths`를 명시하면 소스 상대 경로와 중첩 압축 항목 이름을 추가한다. 파일과 압축 항목 이름도 공유 전에 확인한다. 로그의 이벤트와 메시지는 UI 언어와 독립적인 영어다.

로그 파일을 보관하려면 위 리디렉션이나 서비스 관리자의 수집·회전을 설정한다. 앱이 로그 파일을 자동 생성·회전하지 않는다. 출력 큐는 256개, 레코드는 최대 4 KiB이며 출력 장애나 큐 포화 시 로그가 유실될 수 있다. 복구 후 `logging.dropped`, 반복 억제 시 `logging.suppressed`로 개수를 확인한다. SQLite의 작업 상태가 복구의 기준이며 로그는 감사 기록이나 영구 이력이 아니다. 상세 정책과 검증 범위는 [로그 추가 계획·구현 기록](logging-plan.md)을 따른다.

## 선택적 비밀번호와 외부 접속

로컬 앱에 비밀번호를 적용하려면 `--password-env MY_GALLERY_PASSWORD`를 지정한다. 실제 비밀번호는 해당 환경 변수에서 읽고 최소 16바이트를 요구한다. 외부 공개 여부는 이용자 선택이며 설치 사이트의 도메인을 앱 주소로 사용하지 않는다.

이용자가 자신의 HTTPS 프록시 뒤에서 앱을 공개하는 예:

```powershell
$env:VGALARY_WEB_PASSWORD = '여기에-충분히-긴-개인-비밀번호'
node scripts/deploy.mjs --domain gallery.example.com
```

`--domain`을 명시하면 HTTPS 공개 Origin과 비밀번호 인증을 설정한다. 이때 기본 비밀번호 환경 변수 이름은 `VGALARY_WEB_PASSWORD`이며 `--password-env`로 바꿀 수 있다. 이용자의 프록시가 `https://gallery.example.com`의 요청을 로컬 리스너로 전달한다. 쿠키는 HttpOnly·SameSite=Strict이며 도메인 모드는 Secure이므로 HTTPS 주소로 로그인한다. 세션은 12시간 유효하고 비밀번호 변경 후 재시작하면 이전 서명 세션은 무효다. 앱 설정이나 빌드 기록에는 비밀번호 값을 저장하지 않는다.

실행 파일을 직접 사용하는 경우에도 `-public-origin https://gallery.example.com -web-password-env VGALARY_WEB_PASSWORD`로 같은 모드를 선택한다. 기본 리스너는 loopback을 유지하며 직접 LAN 주소에 바인딩하지 않는다. 프록시는 Host·HTTP Range를 보존하고 모델 응답을 기다릴 수 있는 제한 시간을 사용한다. DNS·TLS·터널·서비스 관리자와 외부 서비스 포트 공개는 이용자가 관리한다.

## 로컬 웹 개발

```text
npm ci --prefix web
npm run build --prefix web
go run ./cmd/vgalary -data-dir .local/app
```

웹 UI와 API를 함께 제공한다. 프런트엔드 개발 서버가 필요하면 백엔드를 실행한 상태에서 `npm run dev --prefix web`을 사용한다. Vite의 `/api` 프록시는 기본 백엔드로 전달한다.
