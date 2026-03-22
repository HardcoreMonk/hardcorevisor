// errors.go — 구조화된 API 에러 응답 및 유효성 검사 헬퍼.
//
// 모든 API 에러 응답은 APIError 형식으로 일관되게 반환된다.
// 에러 코드 상수(ErrCode*)를 사용하여 클라이언트가 에러 유형을 프로그래밍적으로 구분할 수 있다.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// APIError — 구조화된 에러 응답.
//
// JSON 형식으로 직렬화되어 클라이언트에 반환된다.
//   - Error: 사람이 읽을 수 있는 에러 메시지
//   - Code: 프로그래밍적 에러 분류 코드 (ErrCode* 상수)
//   - Details: 추가 디버그 정보 (선택적)
type APIError struct {
	Error   string `json:"error"`
	Code    string `json:"code"`
	Details string `json:"details,omitempty"`
}

// 공통 에러 코드 상수.
// 클라이언트는 이 코드로 에러 유형을 구분하여 적절한 처리를 할 수 있다.
const (
	ErrCodeBadRequest   = "BAD_REQUEST"
	ErrCodeNotFound     = "NOT_FOUND"
	ErrCodeConflict     = "CONFLICT"
	ErrCodeInternal     = "INTERNAL_ERROR"
	ErrCodeUnauthorized = "UNAUTHORIZED"
	ErrCodeForbidden       = "FORBIDDEN"
	ErrCodeTooManyRequests = "TOO_MANY_REQUESTS"
)

// writeError — 구조화된 에러 응답을 JSON으로 작성한다.
//
// # 매개변수
//   - status: HTTP 상태 코드 (400, 404, 409, 500 등)
//   - code: 에러 분류 코드 (ErrCode* 상수)
//   - message: 에러 메시지
//   - details: 추가 디버그 정보 (선택적, 최대 1개)
func writeError(w http.ResponseWriter, status int, code, message string, details ...string) {
	apiErr := APIError{
		Error: message,
		Code:  code,
	}
	if len(details) > 0 {
		apiErr.Details = details[0]
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(apiErr)
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

// validateRange — 정수 값이 지정된 범위 내에 있는지 검사한다.
// 범위를 벗어나면 에러 메시지를 반환한다.
func validateRange(field string, value, min, max int) string {
	if value < min || value > max {
		return fmt.Sprintf("%s must be between %d and %d, got %d", field, min, max, value)
	}
	return ""
}
