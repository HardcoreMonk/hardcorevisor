// disk.go — qcow2/raw 디스크 이미지 생성 유틸리티.
//
// qemu-img 명령어를 사용하여 가상 디스크를 생성한다.
// QEMU Real 모드에서 VM에 연결할 디스크 이미지를 준비하는 데 사용된다.
package storage

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// CreateDisk — qemu-img로 디스크 이미지를 생성한다.
//
// 매개변수:
//   - dir: 디스크 저장 디렉터리 (예: /var/lib/hcv/disks)
//   - name: 디스크 이름 (확장자 자동 추가)
//   - format: "qcow2" 또는 "raw"
//   - sizeGB: 디스크 크기 (GB)
//
// 반환: 생성된 디스크 파일의 절대 경로
func CreateDisk(dir, name, format string, sizeGB int) (string, error) {
	if format == "" {
		format = "qcow2"
	}
	if sizeGB <= 0 {
		return "", fmt.Errorf("disk size must be > 0 GB")
	}

	// 디렉터리 생성 (없으면)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("create disk dir: %w", err)
	}

	ext := format
	if ext == "qcow2" {
		ext = "qcow2"
	}
	path := filepath.Join(dir, fmt.Sprintf("%s.%s", name, ext))

	// qemu-img create -f qcow2 /path/disk.qcow2 10G
	cmd := exec.Command("qemu-img", "create", "-f", format, path, fmt.Sprintf("%dG", sizeGB))
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("qemu-img create: %s: %w", string(out), err)
	}

	return path, nil
}

// DiskExists — 디스크 파일 존재 여부를 확인한다.
func DiskExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// DeleteDisk — 디스크 파일을 삭제한다.
func DeleteDisk(path string) error {
	return os.Remove(path)
}

// DiskInfo — qemu-img info로 디스크 정보를 조회한다.
func DiskInfo(path string) (string, error) {
	cmd := exec.Command("qemu-img", "info", "--output=json", path)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("qemu-img info: %w", err)
	}
	return string(out), nil
}
