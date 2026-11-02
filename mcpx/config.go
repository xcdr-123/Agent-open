// Package mcpx 通过 JSON 配置接入 MCP server：
// 启动子进程、tools/list 拿工具定义、tools/call 转发调用。
// 对 Agent 来说，MCP 工具和本地工具没有区别，最终都转成 tools.ToolDef。
package mcpx

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
)

// ServerConfig 一个 MCP server 的启动配置（兼容 Claude Desktop 的写法）
type ServerConfig struct {
	Command  string            `json:"command"`  // 可执行文件，如 npx
	Args     []string          `json:"args"`     // 启动参数
	Env      map[string]string `json:"env"`      // 额外环境变量，支持 ${VAR}
	Cwd      string            `json:"cwd"`      // 工作目录，可留空
	Disabled bool              `json:"disabled"` // true 时跳过该 server
}

// Config mcp.json 的根结构
type Config struct {
	MCPServers  map[string]ServerConfig `json:"mcpServers"`
	ToolsOnly   []string                `json:"toolsOnly"`   // 白名单，只保留这些工具；空数组表示不限制
	ToolsExclude []string               `json:"toolsExclude"` // 黑名单，始终排除这些工具
}

// FilterTools 按配置对白名单/黑名单做过滤。
// 规则：toolsOnly 非空时只保留其中的名字；toolsExclude 始终生效。
// 两个都空时原样返回。
func (c *Config) FilterTools(defs []string) []string {
	if c == nil {
		return defs
	}
	exclude := make(map[string]bool, len(c.ToolsExclude))
	for _, n := range c.ToolsExclude {
		exclude[n] = true
	}
	only := make(map[string]bool, len(c.ToolsOnly))
	for _, n := range c.ToolsOnly {
		only[n] = true
	}

	var out []string
	for _, d := range defs {
		if exclude[d] {
			continue
		}
		if len(only) > 0 && !only[d] {
			continue
		}
		out = append(out, d)
	}
	return out
}

// FilterByPolicy 根据白/黑名单对工具名做过滤。
// only 非空时只保留其中的名字；exclude 始终生效。
func FilterByPolicy(defs []string, only, exclude []string) []string {
	exMap := make(map[string]bool, len(exclude))
	for _, n := range exclude {
		exMap[n] = true
	}
	onMap := make(map[string]bool, len(only))
	for _, n := range only {
		onMap[n] = true
	}

	var out []string
	for _, d := range defs {
		if exMap[d] {
			continue
		}
		if len(onMap) > 0 && !onMap[d] {
			continue
		}
		out = append(out, d)
	}
	return out
}

// Load 读取并校验 JSON 配置文件
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	for name, s := range cfg.MCPServers {
		if s.Disabled {
			continue
		}
		if s.Command == "" {
			return nil, fmt.Errorf("%s 中的 %s 缺少 command", path, name)
		}
	}
	return &cfg, nil
}

// Servers 返回按名称排序、已展开环境变量、未禁用的 server 列表
func (c *Config) Servers() []NamedServer {
	if c == nil || len(c.MCPServers) == 0 {
		return nil
	}

	names := make([]string, 0, len(c.MCPServers))
	for name, s := range c.MCPServers {
		if s.Disabled {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	servers := make([]NamedServer, 0, len(names))
	for _, name := range names {
		s := c.MCPServers[name]
		args := make([]string, len(s.Args))
		for i, a := range s.Args {
			args[i] = os.ExpandEnv(a)
		}
		env := make(map[string]string, len(s.Env))
		for k, v := range s.Env {
			env[k] = os.ExpandEnv(v)
		}
		servers = append(servers, NamedServer{
			Name:    name,
			Command: os.ExpandEnv(s.Command),
			Args:    args,
			Env:     env,
			Cwd:     os.ExpandEnv(s.Cwd),
		})
	}
	return servers
}

// NamedServer 带名称的 server 配置
type NamedServer struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
	Cwd     string
}
