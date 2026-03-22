// Package logging — Go slog 기반 구조화 로깅 설정
//
// Go 표준 라이브러리 log/slog를 사용하여 구조화된 로깅을 제공한다.
//
// 지원 형식:
//   - "text": 사람이 읽기 쉬운 텍스트 형식 (개발용, 기본값)
//   - "json": JSON 형식 (프로덕션용, 로그 파싱/분석에 적합)
//
// 지원 레벨:
//   - "debug": 디버깅 정보 (가장 상세)
//   - "info": 일반 정보 (기본값)
//   - "warn": 경고
//   - "error": 에러
//
// 환경변수:
//   - HCV_LOG_LEVEL: 로그 레벨 설정
//   - HCV_LOG_FORMAT: 로그 형식 설정
package logging

import (
	"log/slog"
	"os"
)

// Setup 은 지정된 레벨과 형식으로 구조화 로거를 생성하고,
// slog 기본 로거로 설정한 뒤 반환한다.
//
// 호출 시점: Controller 시작 시 main.go에서 1회 호출
// 동시 호출 안전성: slog.SetDefault는 thread-safe
func Setup(level, format string) *slog.Logger {
	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: parseLevel(level)}

	switch format {
	case "json":
		handler = slog.NewJSONHandler(os.Stdout, opts)
	default:
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger
}

// parseLevel 은 문자열 레벨을 slog.Level로 변환한다.
// 알 수 없는 값은 slog.LevelInfo로 폴백한다.
func parseLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
