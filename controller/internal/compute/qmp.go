// qmp.go — QEMU Machine Protocol (QMP) 클라이언트 구현.
//
// QMP는 JSON 기반 프로토콜로, unix socket을 통해 QEMU 프로세스를 제어한다.
//
// # 연결 순서
//
//  1. unix socket에 연결
//  2. QEMU 인사말(greeting) 메시지 수신
//  3. qmp_capabilities 명령으로 명령 모드 진입
//  4. Execute()로 실제 명령 실행 (cont, stop, system_powerdown, quit 등)
//
// # 사용 예시
//
//	client, err := QMPDial("/var/run/hcv/qmp-10000.sock", 5*time.Second)
//	defer client.Close()
//	client.Execute("cont", nil)  // VM 시작
package compute

import (
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// QMPClient — QEMU QMP unix socket에 연결하는 클라이언트.
type QMPClient struct {
	conn net.Conn
}

// QMPResponse — QMP JSON 응답을 나타낸다.
// Return이 있으면 성공, Error가 있으면 실패이다.
type QMPResponse struct {
	Return json.RawMessage `json:"return,omitempty"`
	Error  *QMPError       `json:"error,omitempty"`
}

// QMPError — QMP 프로토콜 에러. Class는 에러 카테고리, Desc는 상세 설명.
type QMPError struct {
	Class string `json:"class"`
	Desc  string `json:"desc"`
}

// QMPDial — QMP unix socket에 연결하고 명령 모드로 진입한다.
// 인사말 수신 후 qmp_capabilities를 자동으로 전송한다.
//
// # 매개변수
//   - socketPath: unix socket 경로 (예: /var/run/hcv/qmp-10000.sock)
//   - timeout: 연결 타임아웃
//
// # 반환값
//   - *QMPClient: 명령 실행 가능한 QMP 클라이언트
//   - error: 연결 실패 또는 인사말/capabilities 에러
func QMPDial(socketPath string, timeout time.Duration) (*QMPClient, error) {
	conn, err := net.DialTimeout("unix", socketPath, timeout)
	if err != nil {
		return nil, fmt.Errorf("QMP connect %s: %w", socketPath, err)
	}
	client := &QMPClient{conn: conn}
	// Read greeting
	if err := client.readGreeting(); err != nil {
		conn.Close()
		return nil, err
	}
	// Send qmp_capabilities to enter command mode
	if err := client.Execute("qmp_capabilities", nil); err != nil {
		conn.Close()
		return nil, fmt.Errorf("QMP capabilities: %w", err)
	}
	return client, nil
}

func (c *QMPClient) readGreeting() error {
	buf := make([]byte, 4096)
	c.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, err := c.conn.Read(buf)
	return err
}

// Execute — QMP 명령을 전송하고 응답을 읽는다.
//
// # 매개변수
//   - command: QMP 명령명 (cont, stop, system_powerdown, quit 등)
//   - args: 명령 인자 (nil 가능)
//
// # 반환값
//   - nil: 명령 성공 (return 응답 수신)
//   - error: 전송/수신/파싱 실패 또는 QMP 에러 응답
func (c *QMPClient) Execute(command string, args map[string]any) error {
	cmd := map[string]any{"execute": command}
	if args != nil {
		cmd["arguments"] = args
	}
	data, _ := json.Marshal(cmd)
	data = append(data, '\n')

	c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := c.conn.Write(data); err != nil {
		return fmt.Errorf("QMP write: %w", err)
	}

	c.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 65536)
	n, err := c.conn.Read(buf)
	if err != nil {
		return fmt.Errorf("QMP read: %w", err)
	}

	var resp QMPResponse
	if err := json.Unmarshal(buf[:n], &resp); err != nil {
		return fmt.Errorf("QMP parse: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("QMP error: %s: %s", resp.Error.Class, resp.Error.Desc)
	}
	return nil
}

// Close — QMP 연결을 닫는다.
func (c *QMPClient) Close() error {
	return c.conn.Close()
}
