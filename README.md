# JARVIS

Windows PC에서 **Spring Boot bootjar**를 올리고, 기동하고, 감시하는 로컬 관리 도구입니다.

하나의 `jarvis.exe`가 Windows 서비스, 웹 관리 UI, 알림 영역(트레이) 아이콘을 모두 담당합니다.
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

## 설치와 실행

1. `jarvis.exe`를 원하는 폴더에 두고 **더블클릭**합니다.
2. 서비스가 없으면 관리자 권한(UAC)을 요청한 뒤 Windows 서비스 `JARVIS`를 등록합니다.
3. 트레이 아이콘이 생기고, 서비스가 시작됩니다. 이후 로그온 때도 아이콘이 자동으로 뜹니다.
4. 브라우저에서 [http://127.0.0.1:9527/](http://127.0.0.1:9527/) 을 엽니다.

최초 로그인 (데이터 폴더에 사용자가 없을 때만):

| 항목 | 값 |
| --- | --- |
| 아이디 | `admin` |
| 비밀번호 | `admin` |

이미 `C:\ProgramData\JARVIS`에 예전 DB가 있으면 그 계정이 그대로입니다.
처음부터 다시 쓰려면 서비스를 제거한 뒤 그 폴더를 지우고 `jarvis.exe`를 다시 실행하세요.

## 트레이

아이콘을 **더블클릭**하거나 **오른쪽 클릭**하면 메뉴가 나옵니다.

| 메뉴 | 동작 |
| --- | --- |
| 서버 실행 | Windows 서비스 시작. 이미 켜져 있으면 비활성 |
| 서버 중지 | Windows 서비스 중지. Java 프로세스는 그대로 |
| 관리자 UI | 브라우저로 관리 화면. **서버가 실행 중일 때만** 표시 |
| 트레이 종료 | 아이콘만 닫음. 서비스와 Java는 계속 실행 |

시작/중지는 서비스 제어입니다. 트레이가 관리 서버를 프로세스 안에 직접 띄우지 않습니다.

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

## 명령줄

```text
jarvis                 미설치면 서비스 등록, 트레이 표시, 서비스 시작
jarvis tray            위와 같음
jarvis run             콘솔에서 직접 실행 (개발용. 서비스와 동시에 쓰지 말 것)
jarvis install         서비스 등록
jarvis uninstall       서비스 제거 (Java와 데이터 폴더는 그대로)
jarvis start|stop      서비스 시작/중지
jarvis status          서비스 상태와 데이터 루트
jarvis version         빌드 정보
```

공통 플래그: `--home <dir>`

`install` / `uninstall` / 최초 더블클릭의 서비스 등록은 관리자 권한이 필요합니다.
등록이 끝난 뒤 트레이의 시작/중지는 일반 사용자로 동작합니다.

## 제거

```powershell
# 1. 트레이 종료 후
.\jarvis.exe uninstall

# 2. 계정·설정·업로드 jar까지 지우려면
Remove-Item -Recurse -Force "$env:ProgramData\JARVIS"
```

`uninstall`은 Java 프로세스를 종료하지 않습니다.

## 빌드

```powershell
.\build\build.ps1          # Go만. 커밋된 UI를 포함
.\build\build.ps1 -Web     # React UI를 다시 빌드한 뒤 exe 생성
```

결과는 `bin\jarvis.exe` 입니다.

관리 UI는 `web/` (React + Vite)에서 빌드되어 `internal/webui/dist`에 들어가고, Go가 embed 합니다.
UI를 건드리지 않으면 Node 없이 `go build ./cmd/jarvis`만으로도 빌드됩니다.

개발 시 핫 리로드:

```powershell
go run .\cmd\jarvis run
cd web; npm run dev          # http://127.0.0.1:5173  → API는 9527로 프록시
```

## 저장소 구조

```text
cmd/jarvis/          진입점 (run / service / tray / install)
internal/api/        REST + 세션
internal/auth/       계정, argon2id, 역할
internal/artifact/   jar 저장소, 버전 승격
internal/supervisor/ 기동·정지·재부착·워치독
internal/runner/     Windows 분리 기동
internal/jdk/        JDK 탐색
internal/tray/       알림 영역 아이콘
internal/winsvc/     Windows 서비스 등록/제어
internal/webui/      embed된 관리 UI
web/                 관리 UI 소스
build/build.ps1      빌드 스크립트
```

## 라이선스

이 저장소에 라이선스 파일이 없으면 저작권은 기여자에게 있습니다. 사용 전에 저장소 정책을 확인하세요.
