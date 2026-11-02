package mcpx

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/openai-go/v3"

	"tui/Agent-open/tools"
)

const (
	connectTimeout = 30 * time.Second
	callTimeout    = 60 * time.Second
)

// Manager 持有所有 MCP 会话，程序退出时统一关闭
type Manager struct {
	sessions []*mcp.ClientSession
}

// Close 关闭全部 MCP 会话并终止子进程
func (m *Manager) Close() {
	if m == nil {
		return
	}
	for _, s := range m.sessions {
		_ = s.Close()
	}
}

// ConnectAll 连接所有 server，把 MCP 工具转成 tools.ToolDef。
// 单个 server 连接失败不会影响其他 server，错误通过 notes 返回。
func ConnectAll(ctx context.Context, cfg *Config) (*Manager, []*tools.ToolDef, []string) {
	mgr := &Manager{}
	var defs []*tools.ToolDef
	var notes []string

	for _, srv := range cfg.Servers() {
		session, err := connect(ctx, srv)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: 连接失败: %v", srv.Name, err))
			continue
		}
		mgr.sessions = append(mgr.sessions, session)

		listCtx, cancel := context.WithTimeout(ctx, connectTimeout)
		list, err := listAllTools(listCtx, session)
		cancel()
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s: 获取工具失败: %v", srv.Name, err))
			_ = session.Close()
			continue
		}

		for _, t := range list {
			defs = append(defs, toToolDef(session, t))
		}
		notes = append(notes, fmt.Sprintf("%s: 已连接，注册 %d 个工具", srv.Name, len(list)))
	}
	return mgr, defs, notes
}

// connect 启动子进程并通过 stdio 建立 MCP 会话
func connect(ctx context.Context, srv NamedServer) (*mcp.ClientSession, error) {
	cmd := exec.Command(resolveCommand(srv.Command), srv.Args...)
	cmd.Dir = srv.Cwd
	cmd.Env = os.Environ()
	for k, v := range srv.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "rate-agent",
		Version: "1.0.0",
	}, nil)
	return client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
}

// resolveCommand 在 Windows 上优先找 .cmd/.exe，
// 避免把 npm 安装的无扩展名 shell 脚本当成可执行文件。
func resolveCommand(name string) string {
	if runtime.GOOS != "windows" || filepath.Ext(name) != "" {
		return name
	}
	for _, ext := range []string{".cmd", ".exe", ".bat"} {
		if p, err := exec.LookPath(name + ext); err == nil {
			return p
		}
	}
	return name
}

// listAllTools 处理 tools/list 的分页，返回全部工具
func listAllTools(ctx context.Context, session *mcp.ClientSession) ([]*mcp.Tool, error) {
	var all []*mcp.Tool
	cursor := ""
	for {
		list, err := session.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, err
		}
		all = append(all, list.Tools...)
		if list.NextCursor == "" {
			return all, nil
		}
		cursor = list.NextCursor
	}
}

// toToolDef 把 MCP 工具定义转成本地 ToolDef，调用时转发 tools/call
func toToolDef(session *mcp.ClientSession, t *mcp.Tool) *tools.ToolDef {
	name := t.Name
	desc := t.Description

	params := openai.FunctionParameters{"type": "object", "properties": map[string]any{}}
	if t.InputSchema != nil {
		if b, err := json.Marshal(t.InputSchema); err == nil {
			var p openai.FunctionParameters
			if err := json.Unmarshal(b, &p); err == nil {
				params = p
			}
		}
	}

	return &tools.ToolDef{
		Name:        name,
		Description: desc,
		Params:      params,
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			callCtx, cancel := context.WithTimeout(ctx, callTimeout)
			defer cancel()

			res, err := session.CallTool(callCtx, &mcp.CallToolParams{
				Name:      name,
				Arguments: args,
			})
			if err != nil {
				return "", err
			}
			out := contentText(res.Content)
			if out == "" && res.StructuredContent != nil {
				if b, err := json.Marshal(res.StructuredContent); err == nil {
					out = string(b)
				}
			}
			if res.IsError {
				return "", fmt.Errorf("%s", out)
			}
			return out, nil
		},
	}
}

// contentText 拼接 MCP 返回的文本内容
func contentText(content []mcp.Content) string {
	parts := make([]string, 0, len(content))
	for _, c := range content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}
