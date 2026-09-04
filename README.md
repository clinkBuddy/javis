# JARVIS

Windows PC에서 **Spring Boot bootjar**를 올리고, 기동하고, 감시하는 로컬 관리 도구입니다.

하나의 `jarvis.exe`가 Windows 서비스와 웹 관리 UI를 담당합니다.
JARVIS 프로세스나 서비스를 중지해도, 이미 띄운 Java 애플리케이션은 그대로 살아 있습니다.

관리 UI 기본 주소: [http://127.0.0.1:9527/](http://127.0.0.1:9527/)

## 무엇을 하나요

- bootjar 업로드, 버전 보관, 활성 버전 승격/롤백
- JDK 자동 탐색과 JVM 옵션(프로파일) 관리
- Windows에서 Java 프로세스를 JARVIS와 **분리**해서 기동 (서비스가 죽어도 Java는 유지)
- 재시작 시 PID·생성시각·마커로 기존 프로세스에 다시 붙음
- 크래시 워치독, autostart 순서
- CPU/메모리 등 리소스와 콘솔 로그 조회
- 세션 로그인, 역할(admin / operator / viewer)

원격 리눅스 호스트 관리는 아직 없습니다. 관리 호스트는 Windows 전용입니다.

## 요구 사항

- Windows 10/11
- 관리할 앱용 JDK (JARVIS 자체는 Java가 필요 없습니다)
- 소스에서 빌드할 때: Go 1.27+, (UI를 고칠 때만) Node.js
- 설치 파일을 만들 때: [NSIS](https://nsis.sourceforge.io/Download) 3.x (`pack.ps1`이 없으면 portable 버전을 받습니다)

## 설치와 실행

1. `bin\JARVIS-Setup-0.1.0.exe`를 실행합니다. 관리자 권한(UAC)이 필요합니다.
2. `C:\Program Files\JARVIS`에 설치되고, Windows 서비스 `JARVIS`가 등록·시작됩니다.
3. 바탕화면에 **JARVIS** 바로가기가 생깁니다.

바로가기를 실행하면:

| 서비스 상태 | 동작 |
| --- | --- |
| 중지됨 | 서비스를 시작합니다 |
| 실행 중 | 관리자 UI를 브라우저로 엽니다 |

최초 로그인 (데이터 폴더에 사용자가 없을 때만):

| 항목 | 값 |
| --- | --- |
| 아이디 | `admin` |
| 비밀번호 | `admin` |

이 비밀번호로는 관리 기능을 쓸 수 없습니다. 로그인 직후 12자 이상의 새 비밀번호를 정해야 합니다. 힌트 문구는 `admin`/`admin`이 아직 유효할 때만 로그인 화면에 나옵니다.

이미 `C:\ProgramData\JARVIS`에 예전 DB가 있으면 그 계정이 그대로입니다.
처음부터 다시 쓰려면 서비스를 제거한 뒤 그 폴더를 지우고 설치 프로그램을 다시 실행하세요.

관리자는 **내 계정** 또는 사용자 화면에서 아이디도 바꿀 수 있습니다.

## 데이터 위치

기본 루트는 `%ProgramData%\JARVIS` (`C:\ProgramData\JARVIS`) 입니다.

```text
config.yaml          설정
jarvis.db            SQLite (계정, 앱, 아티팩트, 세션)
repo/<앱>/<버전>/    업로드된 jar
apps/<앱>/           콘솔 로그, 작업 디렉터리, 실행 정보
logs/                JARVIS 자체 로그
```

다른 폴더를 쓰려면 `JARVIS_HOME` 또는 `--home <dir>`을 사용합니다.

관리 UI를 인터넷에 열면 스캐너 경로(`.env`, `wp-admin` 등)와 연속 로그인 실패가 해당 IP를 **즉시 차단**합니다. 차단된 주소는 HTTP 응답 없이 연결이 끊깁니다. 목록은 관리자 메뉴 **차단 IP**에서 조회·추가·해제합니다. `127.0.0.1`은 차단되지 않습니다.

## 명령줄

```text
jarvis                 서비스가 꺼져 있으면 시작, 켜져 있으면 관리자 UI 열기
jarvis run             콘솔에서 직접 실행 (개발용. 서비스와 동시에 쓰지 말 것)
jarvis install         서비스 등록
jarvis uninstall       서비스 제거 (Java와 데이터 폴더는 그대로)
jarvis start|stop      서비스 시작/중지
jarvis status          서비스 상태와 데이터 루트
jarvis version         빌드 정보
```

공통 플래그: `--home <dir>`

`install` / `uninstall`은 관리자 권한이 필요합니다.
등록이 끝난 뒤 바로가기의 시작/UI 열기는 일반 사용자로 동작합니다.

## 제거

Windows 설정 → 앱에서 JARVIS를 제거하거나, 설치 폴더의 `uninstall.exe`를 실행합니다.

```powershell
# 계정·설정·업로드 jar까지 지우려면 제거 후에
Remove-Item -Recurse -Force "$env:ProgramData\JARVIS"
```

제거는 Java 프로세스를 종료하지 않습니다.

## 빌드

```powershell
.\build\build.ps1          # Go만. 커밋된 UI를 포함
.\build\build.ps1 -Web     # React UI를 다시 빌드한 뒤 exe 생성
.\build\pack.ps1           # NSIS 설치 파일 bin\JARVIS-Setup-<version>.exe
.\build\pack.ps1 -Web      # UI까지 다시 빌드한 뒤 설치 파일
```

결과는 `bin\jarvis.exe` 입니다. 설치 파일은 `bin\JARVIS-Setup-0.1.0.exe` 입니다.

설치 파일은 `C:\Program Files\JARVIS`에 복사하고, Windows 서비스를 등록한 뒤 **바탕화면에 JARVIS 바로가기**를 만듭니다. 시작 메뉴에도 같은 바로가기가 생깁니다.

관리 UI는 `web/` (React + Vite)에서 빌드되어 `internal/webui/dist`에 들어가고, Go가 embed 합니다.
UI를 건드리지 않으면 Node 없이 `go build ./cmd/jarvis`만으로도 빌드됩니다.

개발 시 핫 리로드:

```powershell
go run .\cmd\jarvis run
cd web; npm run dev          # http://127.0.0.1:5173  → API는 9527로 프록시
```

## 저장소 구조

```text
cmd/jarvis/          진입점 (run / service / install / 바로가기)
internal/api/        REST + 세션
internal/auth/       계정, argon2id, 역할
internal/artifact/   jar 저장소, 버전 승격
internal/supervisor/ 기동·정지·재부착·워치독
internal/runner/     Windows 분리 기동
internal/jdk/        JDK 탐색
internal/appicon/    바로가기·설치 프로그램용 아이콘
internal/winsvc/     Windows 서비스 등록/제어
internal/webui/      embed된 관리 UI
web/                 관리 UI 소스
build/build.ps1      빌드 스크립트
build/pack.ps1       NSIS 설치 파일 패키징
build/nsis/          NSIS 스크립트
```

## 라이선스

이 저장소에 라이선스 파일이 없으면 저작권은 기여자에게 있습니다. 사용 전에 저장소 정책을 확인하세요.
