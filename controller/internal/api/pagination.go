package api

import (
	"net/http"
	"strconv"
)

const DefaultLimit = 100
const MaxLimit = 1000

type PaginatedResponse struct {
	Data       any `json:"data"`
	TotalCount int `json:"total_count"`
	Offset     int `json:"offset"`
	Limit      int `json:"limit"`
}

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
