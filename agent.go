package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"tui/Agent-open/core"
	"tui/Agent-open/mcpx"
	"tui/Agent-open/tools"
	"tui/Agent-open/tuiapp"
)

// getLlmTools 获取工具：对应教程里的 tools := []Tool{...}
func getLlmTools() []*tools.ToolDef {
	return []*tools.ToolDef{
		tools.NewGetExchangeRate(),
		tools.NewGetCurrentTime(),
	}
}

func main() {
	ctx := context.Background()

	// 本地工具 + mcp.json 中配置的 MCP 工具
	toolDefs, closeMCP, notes := loadTools(ctx)
	defer closeMCP()
	for _, n := range notes {
		fmt.Println("[mcp]", n)
	}

	// mcp 子命令：直接进入 MCP 工具控制台，无需 API Key
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		runMCPConsole(ctx, toolDefs, notes)
		return
	}

	agent, err := newAgent(toolDefs)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	// tui 子命令：启动聊天式终端界面（Bubble Tea）
	if len(os.Args) > 1 && os.Args[1] == "tui" {
		if err := tuiapp.Run(agent); err != nil {
			fmt.Println("TUI 异常退出:", err)
			os.Exit(1)
		}
		return
	}

	reader := bufio.NewReader(os.Stdin)
	fmt.Println("已进入多轮对话（上下文连续），输入空行或 exit 退出。")

	// 外层对话循环：每轮读一次用户输入，跑完一次完整问答
	for {
		fmt.Print("\n你: ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == "" || input == "exit" || input == "quit" {
			break
		}

		_, err := agent.Ask(ctx, input, func(e core.Event) {
			switch e.Kind {
			case core.EventRound:
				fmt.Println("调用第几轮", e.Text)
			case core.EventReasoning:
				if e.Text != "" {
					fmt.Println("----think:----")
					fmt.Println(e.Text)
					fmt.Println("----think-end----")
				}
			case core.EventToolCall:
				fmt.Println("调用了工具", e.Name)
			case core.EventToolResult:
				fmt.Println("----->", e.Name, e.Text)
			case core.EventAnswer:
				fmt.Println("助手:", e.Text)
			}
		})
		if err != nil {
			fmt.Println("出错:", core.DescribeError(err))
		}
	}
}

// loadTools 加载本地工具，并按 mcp.json 接入 MCP 工具。
// 配置文件不存在时静默跳过，不影响原有功能。
// 顶层 toolsOnly / toolsExclude 对本地 + MCP 工具统一生效。
func loadTools(ctx context.Context) ([]*tools.ToolDef, func(), []string) {
	defs := getLlmTools()
	var cfg *mcpx.Config
	var mgr *mcpx.Manager
	var mcpTools []*tools.ToolDef
	var notes []string

	path := envOr("MCP_CONFIG", "mcp.json")
	var loadErr error
	cfg, loadErr = mcpx.Load(path)
	if loadErr != nil {
		if !os.IsNotExist(loadErr) {
			notes = append(notes, "读取 "+path+" 失败: "+loadErr.Error())
		}
	} else {
		mgr, mcpTools, notes = mcpx.ConnectAll(ctx, cfg)
	}

	// 合并本地 + MCP
	all := append(defs, mcpTools...)

	// 顶层 toolsOnly / toolsExclude 过滤
	if cfg != nil && (len(cfg.ToolsOnly) > 0 || len(cfg.ToolsExclude) > 0) {
		filtered := mcpx.FilterByPolicy(defNames(all), cfg.ToolsOnly, cfg.ToolsExclude)
		keep := make(map[string]bool, len(filtered))
		for _, n := range filtered {
			keep[n] = true
		}
		var out []*tools.ToolDef
		for _, d := range all {
			if keep[d.Name] {
				out = append(out, d)
			}
		}
		notes = append(notes, fmt.Sprintf("工具过滤: %d → %d（toolsOnly=%d toolsExclude=%d）",
			len(all), len(out), len(cfg.ToolsOnly), len(cfg.ToolsExclude)))
		all = out
	}

	closer := func() {
		if mgr != nil {
			mgr.Close()
		}
	}
	return all, closer, notes
}

// defNames 提取 ToolDef 名字列表。
func defNames(defs []*tools.ToolDef) []string {
	names := make([]string, len(defs))
	for i, d := range defs {
		names[i] = d.Name
	}
	return names
}

// newAgent 从环境变量读取配置并创建代理
func newAgent(toolDefs []*tools.ToolDef) (*core.Agent, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf(`请先设置环境变量 OPENAI_API_KEY，例如：
  临时(当前终端): $env:OPENAI_API_KEY="sk-xxx"
  永久: [Environment]::SetEnvironmentVariable('OPENAI_API_KEY','sk-xxx','User')`)
	}

	baseURL := envOr("OPENAI_BASE_URL", "https://token.sensenova.cn/v1")
	model := envOr("OPENAI_MODEL", "sensenova-6.8-flash-lite")
	return core.NewAgent(apiKey, baseURL, model, toolDefs), nil
}

// envOr 读取环境变量，为空时返回默认值。
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// runMCPConsole 进入 MCP 工具控制台，可在终端直接调用已接入的工具。
// 用法：
//
//	mcp> list                        列出所有工具
//	mcp> <工具名>                     调用工具（参数默认为 {}）
//	mcp> <工具名> {"key":"value"}    调用工具并传入 JSON 参数
//	mcp> help                        查看帮助
//	mcp> exit / quit                 退出
func runMCPConsole(ctx context.Context, toolDefs []*tools.ToolDef, notes []string) {
	toolMap := make(map[string]*tools.ToolDef, len(toolDefs))
	for _, t := range toolDefs {
		toolMap[t.Name] = t
	}

	fmt.Println("=== MCP 工具控制台 ===")
	if len(notes) > 0 {
		for _, n := range notes {
			fmt.Println(n)
		}
	}
	fmt.Printf("共加载 %d 个工具（本地 + MCP）\n", len(toolDefs))
	listTools(toolDefs)
	fmt.Println("\n输入 list 查看工具列表，exit 退出。")
	fmt.Println("调用方式：工具名 {json参数}，例如：")
	fmt.Println(`  list_directory {"path":"C:/Users/16902/Desktop/Agent-open"}`)

	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Print("\nmcp> ")
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			fmt.Println("bye.")
			return
		}
		if line == "list" {
			listTools(toolDefs)
			continue
		}
		if line == "help" {
			fmt.Println(`命令:
  list                    列出所有工具
  help                    显示帮助
  exit / quit             退出
  <工具名>                 调用工具，参数默认 {}
  <工具名> {"k":"v"}       调用工具并传入 JSON 参数`)
			continue
		}

		parts := strings.SplitN(line, " ", 2)
		name := parts[0]
		args := "{}"
		if len(parts) == 2 {
			args = strings.TrimSpace(parts[1])
		}

		tool, ok := toolMap[name]
		if !ok {
			fmt.Printf("未知工具: %s（输入 list 查看可用工具）\n", name)
			continue
		}

		result, err := tool.Call(ctx, args)
		if err != nil {
			fmt.Printf("[错误] %s\n", err)
			continue
		}
		fmt.Println(result)
	}
}

// listTools 打印工具列表（名称 + 描述）。
func listTools(toolDefs []*tools.ToolDef) {
	fmt.Println("\n可用工具:")
	for _, t := range toolDefs {
		fmt.Printf("  %-30s %s\n", t.Name, t.Description)
	}
}
