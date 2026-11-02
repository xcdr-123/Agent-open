// Package tuiapp 提供聊天式汇率助手终端界面（Bubble Tea）。
// 通过 go run . tui 启动；像聊天软件一样输入自然语言，回车发送，
// 模型回答过程（思考、工具调用）实时显示在对话流中。
package tuiapp

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"tui/Agent-open/core"
)

// ---------- 样式 ----------

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205")).
			Padding(0, 1)

	userLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("39"))

	assistantLabelStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("212"))

	toolStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	errorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("196"))

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241"))

	promptStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("39"))
)

// ---------- 对话条目 ----------

type role string

const (
	roleUser      role = "user"
	roleAssistant role = "assistant"
	roleTool      role = "tool"
	roleError     role = "error"
	roleSystem    role = "system"
)

type entry struct {
	role role
	text string
}

// ---------- 模型 ----------

type model struct {
	agent    *core.Agent
	input    textinput.Model
	viewport viewport.Model
	spin     spinner.Model

	entries []entry
	ch      chan core.Event
	loading bool
	status  string

	width  int
	height int
	ready  bool
}

func newModel(agent *core.Agent) model {
	in := textinput.New()
	in.Placeholder = "问点什么，例如：100 美元能换多少人民币？"
	in.CharLimit = 200
	in.Prompt = ""
	// 在构造时聚焦：Init 是值接收者，若放在 Init 里聚焦会作用在副本上而失效
	in.Focus()

	sp := spinner.New()
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	return model{
		agent: agent,
		input: in,
		spin:  sp,
		entries: []entry{
			{role: roleSystem, text: "你好，我是汇率助手。直接输入问题即可，例如「100 人民币换多少日元」。数据来自欧央行参考汇率（仅工作日）。"},
		},
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.spin.Tick)
}

// ---------- 命令 ----------

// listen 持续读取 Agent 事件，直到收到最终回答或错误。
func listen(ch <-chan core.Event) tea.Cmd {
	return func() tea.Msg {
		e, ok := <-ch
		if !ok {
			return nil
		}
		return e
	}
}

// askCmd 后台执行一轮问答，事件经 ch 送回界面。
func askCmd(agent *core.Agent, ch chan core.Event, input string) tea.Cmd {
	return func() tea.Msg {
		go func() {
			defer close(ch)
			_, err := agent.Ask(context.Background(), input, func(e core.Event) {
				ch <- e
			})
			if err != nil {
				ch <- core.Event{Kind: core.EventError, Text: core.DescribeError(err)}
			}
		}()
		return <-ch
	}
}

// ---------- 更新 ----------

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if !m.ready {
			m.viewport = viewport.New(msg.Width, m.viewportHeight())
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = m.viewportHeight()
		}
		m.input.Width = msg.Width - 6
		m.refresh()
		return m, nil

	case spinner.TickMsg:
		if m.loading {
			var cmd tea.Cmd
			m.spin, cmd = m.spin.Update(msg)
			return m, cmd
		}
		return m, nil

	case core.Event:
		switch msg.Kind {
		case core.EventRound:
			if msg.Text == "0" {
				m.status = "正在思考…"
			}
		case core.EventReasoning:
			// 思考摘要不展示，避免干扰对话阅读
		case core.EventToolCall:
			m.status = "正在调用 " + msg.Name + "…"
			m.entries = append(m.entries, entry{role: roleTool, text: "调用工具 " + msg.Name})
			m.refresh()
		case core.EventToolResult:
			m.entries = append(m.entries, entry{role: roleTool, text: msg.Text})
			m.refresh()
		case core.EventAnswer:
			m.loading = false
			m.status = ""
			m.entries = append(m.entries, entry{role: roleAssistant, text: msg.Text})
			m.refresh()
			return m, nil
		case core.EventError:
			m.loading = false
			m.status = ""
			m.entries = append(m.entries, entry{role: roleError, text: msg.Text})
			m.refresh()
			return m, nil
		}
		return m, listen(m.ch)

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyEnter:
			if m.loading {
				return m, nil
			}
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil
			}
			if text == "exit" || text == "quit" {
				return m, tea.Quit
			}

			m.input.SetValue("")
			m.entries = append(m.entries, entry{role: roleUser, text: text})
			m.loading = true
			m.status = "正在思考…"
			m.ch = make(chan core.Event, 64)
			m.refresh()

			return m, tea.Batch(askCmd(m.agent, m.ch, text), m.spin.Tick)
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// viewportHeight 计算对话区高度，预留标题、状态、输入框与页脚。
func (m model) viewportHeight() int {
	h := m.height - 8
	if h < 3 {
		h = 3
	}
	return h
}

// refresh 重绘对话内容并滚到底部。
func (m *model) refresh() {
	if !m.ready {
		return
	}
	m.viewport.SetContent(m.transcript())
	m.viewport.GotoBottom()
}

// ---------- 视图 ----------

func (m model) transcript() string {
	w := m.width - 6
	if w < 20 {
		w = 20
	}
	wrap := lipgloss.NewStyle().Width(w - 2)

	var b strings.Builder
	for i, e := range m.entries {
		if i > 0 {
			b.WriteString("\n")
		}
		switch e.role {
		case roleUser:
			b.WriteString(userLabelStyle.Render("你"))
			b.WriteString("\n")
			b.WriteString(wrap.Render(e.text))
		case roleAssistant:
			b.WriteString(assistantLabelStyle.Render("助手"))
			b.WriteString("\n")
			b.WriteString(wrap.Render(e.text))
		case roleTool:
			b.WriteString(toolStyle.Render(wrap.Render("· " + e.text)))
		case roleError:
			b.WriteString(errorStyle.Render(wrap.Render("✗ " + e.text)))
		case roleSystem:
			b.WriteString(dimStyle.Render(wrap.Render(e.text)))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (m model) View() string {
	if !m.ready {
		return "正在初始化界面…"
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("⇄ 汇率助手"))
	b.WriteString(dimStyle.Render(" 聊天模式 · 欧央行参考汇率"))
	b.WriteString("\n")
	b.WriteString(m.viewport.View())
	b.WriteString("\n")

	if m.loading {
		b.WriteString(m.spin.View())
		b.WriteString(" ")
		b.WriteString(m.status)
	} else {
		b.WriteString(dimStyle.Render("就绪"))
	}
	b.WriteString("\n")
	b.WriteString(promptStyle.Render("你: "))
	b.WriteString(m.input.View())
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("Enter 发送 · Ctrl+C/Esc 退出 · 支持中文币种"))
	return b.String()
}

// Run 启动聊天式 TUI（alt screen 全屏模式）。
func Run(agent *core.Agent) error {
	p := tea.NewProgram(newModel(agent), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
