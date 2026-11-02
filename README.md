# Agent-open

一个用 Go 实现的汇率助手 Agent，严格遵循 [SuiYiDian Agent 教程](https://zx.suiyidian.cn/agent-go.html) 的工具调用骨架。

支持三种运行模式（CLI 对话、聊天 TUI、MCP 工具测试台），可通过 `mcp.json` 接入任意 MCP server。

## 功能

- **汇率换算**：欧央行（ECB）参考汇率，支持中文币种别名（人民币、美元、日元、欧元…）、金额换算、历史汇率查询
- **时间查询**：获取本地日期时间
- **MCP 扩展**：通过 JSON 配置文件接入 filesystem、playwright 等 MCP server
- **工具精简**：`toolsOnly` / `toolsExclude` 白黑名单过滤工具列表

## 环境要求

- Go 1.26+
- Node.js（MCP server 通过 `npx` 启动）
- 支持 OpenAI 兼容接口的 LLM（默认 SenseNova）

## 环境变量

| 变量 | 必填 | 默认值 | 说明 |
|------|------|--------|------|
| `OPENAI_API_KEY` | ✅ | — | LLM 接口 Key |
| `OPENAI_BASE_URL` | ❌ | `https://token.sensenova.cn/v1` | 接口地址 |
| `OPENAI_MODEL` | ❌ | `sensenova-6.8-flash-lite` | 模型名 |
| `MCP_CONFIG` | ❌ | `mcp.json` | MCP 配置文件路径 |
| `ssl.crt` | ❌ | 工作目录根 | 自定义 CA 证书（TLS 拦截场景） |

PowerShell 临时设置：

```powershell
$env:OPENAI_API_KEY = "sk-xxx"
```

## 三个入口

### 1. CLI 对话（默认）

```bash
go run .
```

进入多轮对话循环，输入空行或 `exit` 退出。LLM 自动根据上下文决定是否调用工具。

### 2. 聊天式 TUI

```bash
go run . tui
```

基于 [Bubble Tea](https://github.com/charmbracelet/bubbletea) 的交互式聊天界面，支持多行输出、推理过程显示。

### 3. MCP 工具测试台（无需 API Key）

```bash
go run . mcp
```

直接调用已接入的工具，纯工具执行不经过 LLM。常用命令：

```
mcp> list                                                              # 列出所有可用工具
mcp> help                                                              # 查看帮助
mcp> get_exchange_rate {"base_currency":"USD","target_currency":"CNY"}
mcp> list_directory {"path":"C:/Users/16902/Desktop/Agent-open"}
mcp> browser_navigate {"url":"https://www.example.com"}
mcp> browser_take_screenshot {}
mcp> exit                                                              # 退出
```

## MCP 配置

配置文件 `mcp.json`，启动时自动加载。顶层字段：

```jsonc
{
  "toolsOnly": ["get_exchange_rate", "browser_navigate"],   // 白名单，只保留这些工具
  "toolsExclude": ["browser_run_code_unsafe"],              // 黑名单，始终排除
  "mcpServers": {
    "filesystem": {                                         // 内置的 filesystem server
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "C:\\path"],
      "env": {}
    },
    "playwright": {                                         // 内置的浏览器自动化
      "command": "npx",
      "args": ["-y", "@playwright/mcp@latest"],
      "env": {}
    },
    "github": {                                             // 可选的 GitHub server（已禁用）
      "disabled": true,
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": {
        "GITHUB_PERSONAL_ACCESS_TOKEN": "${GITHUB_TOKEN}"  // 支持 ${VAR} 展开
      }
    }
  }
}
```

### 工具过滤规则

- `toolsOnly` 非空 → 只保留其中的工具名（最严格）
- `toolsExclude` 非空 → 从结果中再排除这些名字
- 两者都空 → 不过滤，全部加载
- 过滤对象是**本地工具 + MCP 工具的合并列表**

### 添加自定义 MCP server

在 `mcpServers` 里追加一条，重启程序即可：

```jsonc
"brave-search": {
  "command": "npx",
  "args": ["-y", "@brave/mcp-server-search"],
  "env": {
    "BRAVE_API_KEY": "your-key"
  }
}
```

## 项目结构

```
Agent-open/
├── agent.go                入口（CLI / TUI / MCP 控制台三个子命令）
├── core/
│   ├── agent.go            Agent 循环：LLM 调用 → tool_use → 回填 → MaxLoop 保护
│   └── session.go          消息上下文管理
├── tools/
│   ├── tool.go             ToolDef 接口：Name / Description / Params / Fn / Call()
│   ├── get_exchange_rate   汇率工具（欧央行 frankfurter.dev，含中文币种别名）
│   └── get_currentTime    时间工具
├── mcpx/
│   ├── config.go           mcp.json 加载、${VAR} 展开、白黑名单过滤
│   └── client.go           MCP server 连接、tools/list 分页、tools/call 转发
├── tuiapp/
│   └── tui.go              Bubble Tea 聊天式前端
├── mcp.json                MCP server 配置文件
├── ssl.crt                 可选自定义 CA 证书
└── go.mod
```

## 核心设计

### Agent 循环

严格按 SuiYiDian 教程骨架：

```
for round := 0; round < MaxLoop; round++ {
    resp := chat(messages)
    if no tool_use in resp {
        return answer  // 终止条件
    }
    append assistant message
    for each tool_use {
        result := runTool(tool_use)
        append tool_result message
    }
}
return "max loop exceeded"
```

### 工具统一接口

本地工具和 MCP 工具都转成 `tools.ToolDef`，Agent 不感知来源差异：

```go
type ToolDef struct {
    Name        string
    Description string
    Params      openai.FunctionParameters
    Fn          func(ctx, argsJSON) (string, error)
}
```

## 编译 & 运行

```bash
# 构建
go build -o agent.exe

# 三种模式
.\agent.exe              # CLI 对话
.\agent.exe tui          # 聊天 TUI
.\agent.exe mcp          # MCP 工具测试台
```

## 常见问题

### MCP server 连不上

- 确认 `command` 在 PATH 里能找到（Windows 下 `npx` 会自动解析 `.cmd`）
- 查看启动日志 `[mcp] <server>: ...` 具体报错
- `mcp.json` 里 server 名重复或 args 顺序错误

### 工具太多

改 `mcp.json` 的 `toolsOnly` / `toolsExclude`，重启自动生效。

### TLS 证书问题

把公司 CA 证书保存为 `ssl.crt` 放在项目根目录，`tools/get_exchange_rate.go` 会自动追加到信任池。

### SenseNova 401

检查环境变量是否有残留的 `OPENAI_BASE_URL` 指向其他服务商：

```powershell
$env:OPENAI_BASE_URL   # 应为空或 https://token.sensenova.cn/v1
$env:OPENAI_MODEL      # 应为空或 sensenova-6.8-flash-lite
```
