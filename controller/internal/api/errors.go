// errors.go — RFC 7807 Problem Details 기반 API 에러 응답.
//
// 모든 API 에러는 RFC 7807 (application/problem+json) 형식으로 반환된다.
// 기존 code 필드도 확장 필드로 유지하여 하위 호환성을 보장한다.
package api

import (
	"encoding/json"
	"net/http"
)

// ProblemDetail — RFC 7807 Problem Details 응답 구조체.
//
// 표준 필드:
//   - Type: 에러 유형 URI (예: "/errors/not-found")
//   - Title: 짧은 요약 (예: "Not Found")
//   - Status: HTTP 상태 코드
//   - Detail: 상세 설명 (선택적)
//   - Instance: 발생한 요청 URI (선택적)
//
// 확장 필드:
//   - Code: 프로그래밍적 에러 분류 코드 (하위 호환)
type ProblemDetail struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
	Code     string `json:"code,omitempty"`
}

// APIError — ProblemDetail의 별칭 (하위 호환).
type APIError = ProblemDetail

// 공통 에러 코드 상수.
// 클라이언트는 이 코드로 에러 유형을 구분하여 적절한 처리를 할 수 있다.
const (
	ErrCodeBadRequest      = "BAD_REQUEST"
	ErrCodeNotFound        = "NOT_FOUND"
	ErrCodeConflict        = "CONFLICT"
	ErrCodeInternal        = "INTERNAL_ERROR"
	ErrCodeUnauthorized    = "UNAUTHORIZED"
	ErrCodeForbidden       = "FORBIDDEN"
	ErrCodeTooManyRequests = "TOO_MANY_REQUESTS"
)

// errCodeToType — 에러 코드를 RFC 7807 type URI로 매핑한다.
var errCodeToType = map[string]string{
	ErrCodeBadRequest:      "/errors/bad-request",
	ErrCodeNotFound:        "/errors/not-found",
	ErrCodeConflict:        "/errors/conflict",
	ErrCodeInternal:        "/errors/internal",
	ErrCodeUnauthorized:    "/errors/unauthorized",
	ErrCodeForbidden:       "/errors/forbidden",
	ErrCodeTooManyRequests: "/errors/too-many-requests",
}

// errCodeToTitle — 에러 코드를 사람이 읽을 수 있는 제목으로 매핑한다.
var errCodeToTitle = map[string]string{
	ErrCodeBadRequest:      "Bad Request",
	ErrCodeNotFound:        "Not Found",
	ErrCodeConflict:        "Conflict",
	ErrCodeInternal:        "Internal Server Error",
	ErrCodeUnauthorized:    "Unauthorized",
	ErrCodeForbidden:       "Forbidden",
	ErrCodeTooManyRequests: "Too Many Requests",
}

// writeError — RFC 7807 Problem Details 형식으로 에러 응답을 작성한다.
//
// # 매개변수
//   - status: HTTP 상태 코드 (400, 404, 409, 500 등)
//   - code: 에러 분류 코드 (ErrCode* 상수)
//   - message: 상세 에러 메시지
//   - details: 추가 디버그 정보 (선택적, 최대 1개 — detail 필드에 병합)
func writeError(w http.ResponseWriter, status int, code, message string, details ...string) {
	typeURI := errCodeToType[code]
	if typeURI == "" {
		typeURI = "/errors/unknown"
	}
	title := errCodeToTitle[code]
	if title == "" {
		title = http.StatusText(status)
	}

	detail := message
	if len(details) > 0 && details[0] != "" {
		detail = message + ": " + details[0]
	}

	problem := ProblemDetail{
		Type:   typeURI,
		Title:  title,
		Status: status,
		Detail: detail,
		Code:   code,
	}

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(problem)
}

// validateRequired — 필수 필드가 비어 있는지 검사한다.
// 비어 있는 필드가 있으면 "{필드명} is required" 메시지를 반환한다.
// 모든 필드가 유효하면 빈 문자열을 반환한다.
func validateRequired(fields map[string]string) string {
	for field, value := range fields {
		if value == "" {
			return field + " is required"
		}
	}
	return ""
}


