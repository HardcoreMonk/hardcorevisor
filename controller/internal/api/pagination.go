// pagination.go — 목록 API의 오프셋 기반 페이지네이션 지원.
//
// 쿼리 파라미터 ?offset=N&limit=N 으로 페이지를 지정한다.
// 기본 limit: 100, 최대 limit: 1000
package api

import (
	"net/http"
	"strconv"
)

// DefaultLimit — limit 미지정 시 기본값 (100개)
const DefaultLimit = 100

// MaxLimit — limit 최대값 (1000개). 이를 초과하면 MaxLimit으로 제한된다.
const MaxLimit = 1000

// PaginatedResponse — 페이지네이션된 응답 래퍼.
// Data에 실제 데이터 슬라이스, TotalCount에 전체 개수를 포함한다.
type PaginatedResponse struct {
	Data       any `json:"data"`
	TotalCount int `json:"total_count"`
	Offset     int `json:"offset"`
	Limit      int `json:"limit"`
}

// parsePagination — 요청의 쿼리 파라미터에서 offset과 limit을 파싱한다.
// offset < 0이면 0으로 보정, limit이 0 이하이면 DefaultLimit, MaxLimit 초과 시 MaxLimit으로 제한.
func parsePagination(r *http.Request) (offset, limit int) {
	offset, _ = strconv.Atoi(r.URL.Query().Get("offset"))
	limit, _ = strconv.Atoi(r.URL.Query().Get("limit"))
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	return
}

// paginate — 슬라이스와 메타데이터를 PaginatedResponse로 래핑한다.
func paginate(items any, total, offset, limit int) PaginatedResponse {
	return PaginatedResponse{
		Data:       items,
		TotalCount: total,
		Offset:     offset,
		Limit:      limit,
	}
}

// paginateSlice applies offset/limit to a generic slice via reflection or type assertion.
// For simplicity, we'll use it per-handler with typed slices.
