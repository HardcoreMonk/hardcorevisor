// metrics 패키지 유닛 테스트
//
// 테스트 대상: normalizePath() — 동적 경로 파라미터 정규화
package metrics

import (
	"testing"
)

// TestNormalizePath — 동적 경로 파라미터가 플레이스홀더로 치환되는지 검증
func TestNormalizePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want string
	}{
		// VM 경로 — 숫자 ID
		{"VM 상세", "/api/v1/vms/123", "/api/v1/vms/{id}"},
		{"VM 액션", "/api/v1/vms/42", "/api/v1/vms/{id}"},

		// 문자열 ID 경로
		{"디바이스", "/api/v1/devices/gpu-0", "/api/v1/devices/{id}"},
		{"백업", "/api/v1/backups/backup-1", "/api/v1/backups/{id}"},
		{"스냅샷", "/api/v1/snapshots/snap-1", "/api/v1/snapshots/{id}"},
		{"템플릿", "/api/v1/templates/tpl-1", "/api/v1/templates/{id}"},
		{"이미지", "/api/v1/images/img-1", "/api/v1/images/{id}"},
		{"태스크", "/api/v1/tasks/task-1", "/api/v1/tasks/{id}"},
		{"볼륨", "/api/v1/storage/volumes/vol-1", "/api/v1/storage/volumes/{id}"},
		{"펜싱", "/api/v1/cluster/fence/node-03", "/api/v1/cluster/fence/{node}"},
		{"사용자", "/api/v1/auth/users/admin", "/api/v1/auth/users/{username}"},
		{"노드", "/api/v1/nodes/node-1", "/api/v1/nodes/{id}"},
		{"Zone", "/api/v1/network/zones/vxlan-zone", "/api/v1/network/zones/{name}"},

		// 정규화 불필요 경로 (그대로 반환)
		{"헬스체크", "/healthz", "/healthz"},
		{"메트릭", "/metrics", "/metrics"},
		{"VM 목록", "/api/v1/vms", "/api/v1/vms"},
		{"풀 목록", "/api/v1/storage/pools", "/api/v1/storage/pools"},
		{"버전", "/api/v1/version", "/api/v1/version"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := normalizePath(tc.path)
			if got != tc.want {
				t.Errorf("normalizePath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}
