package compute

import (
	"encoding/json"
	"fmt"
	"net"
	"time"
)

// QMPClient connects to a QEMU QMP unix socket
type QMPClient struct {
	conn net.Conn
}

// QMPResponse represents a QMP JSON response
type QMPResponse struct {
	Return json.RawMessage `json:"return,omitempty"`
	Error  *QMPError       `json:"error,omitempty"`
}

// QMPError holds a QMP protocol error
type QMPError struct {
	Class string `json:"class"`
	Desc  string `json:"desc"`
}

// QMPDial connects to a QMP unix socket
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

// Execute sends a QMP command and reads the response
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

// Close closes the QMP connection
func (c *QMPClient) Close() error {
	return c.conn.Close()
}
