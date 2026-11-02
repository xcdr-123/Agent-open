package core

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"tui/Agent-open/tools"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

// MaxLoop 单次问答内模型与工具交互的最大轮数（防止模型无限循环）
const MaxLoop = 10

// DefaultSystemPrompt 汇率助手系统提示词
const DefaultSystemPrompt = "你是人类专业的汇率助手;回答简洁干练,只回答和用户的问题相关的问题。根据工具返回内容事实回答;think也要中文输出。汇率数据来自欧央行参考汇率,仅包含工作日数据,周末与节假日会自动取最近一个工作日,回答时注意说明汇率日期。货币一律使用 ISO 4217 代码。"

// EventKind 事件类型，供界面展示模型思考与工具调用过程
type EventKind string

const (
	EventRound      EventKind = "round"       // 开始新一轮模型调用
	EventReasoning  EventKind = "reasoning"   // 模型思考摘要
	EventToolCall   EventKind = "tool_call"   // 调用工具
	EventToolResult EventKind = "tool_result" // 工具返回
	EventAnswer     EventKind = "answer"      // 最终回答
	EventError      EventKind = "error"       // 出错
)

// Event 一次问答过程中的实时事件
type Event struct {
	Kind EventKind
	Name string // 工具名
	Text string // 内容
}

// toolUse 本轮模型发起的一次工具调用
type toolUse struct {
	callID    string
	name      string
	arguments string
}

// Agent 封装 system 提示词、tools 列表与 messages 上下文
type Agent struct {
	client      openai.Client
	session     *Session
	tools       []*tools.ToolDef
	openaiTools []responses.ToolUnionParam
	model       string
}

// NewAgent 创建带系统提示词的对话代理
func NewAgent(apiKey, baseURL, model string, toolDefs []*tools.ToolDef) *Agent {
	openaiTools := make([]responses.ToolUnionParam, 0, len(toolDefs))
	for _, t := range toolDefs {
		openaiTools = append(openaiTools, t.ToParam())
	}

	session := NewSession()
	session.AddMessage(
		responses.ResponseInputItemParamOfMessage(
			DefaultSystemPrompt,
			responses.EasyInputMessageRoleSystem),
	)

	return &Agent{
		client: openai.NewClient(
			option.WithAPIKey(apiKey),
			option.WithBaseURL(baseURL),
			option.WithRequestTimeout(60*time.Second),
		),
		session:     session,
		tools:       toolDefs,
		openaiTools: openaiTools,
		model:       model,
	}
}

// Ask 发起一轮问答，对应教程里的 Agent Loop：
//
//	messages += user
//	for {
//	    resp = chat(sys, tools, messages)
//	    messages += assistant
//	    if 没有 tool_use { break }
//	    results = 执行每个工具
//	    messages += tool_result
//	}
func (a *Agent) Ask(ctx context.Context, question string, onEvent func(Event)) (string, error) {
	emit := func(e Event) {
		if onEvent != nil {
			onEvent(e)
		}
	}

	// 用户输入进入上下文
	a.session.AddMessage(
		responses.ResponseInputItemParamOfMessage(question, responses.EasyInputMessageRoleUser),
	)

	for round := 0; ; round++ {
		if round > MaxLoop {
			return "", fmt.Errorf("已达到最大工具调用轮数 %d", MaxLoop)
		}
		emit(Event{Kind: EventRound, Text: strconv.Itoa(round)})

		// 1. 调模型
		resp, err := a.chat(ctx)
		if err != nil {
			return "", err
		}

		if text := reasoningText(resp); text != "" {
			emit(Event{Kind: EventReasoning, Text: text})
		}

		// 2. assistant 的工具调用写回上下文
		uses := toolUses(resp)
		a.appendAssistant(uses)

		// 3. 没有 tool_use，说明模型给出了最终回答
		if len(uses) == 0 {
			answer := resp.OutputText()
			// 助手回复写回上下文，下一轮才能记住上文
			a.session.AddMessage(
				responses.ResponseInputItemParamOfMessage(answer, responses.EasyInputMessageRoleAssistant),
			)
			emit(Event{Kind: EventAnswer, Text: answer})
			return answer, nil
		}

		// 4. 逐个执行工具，收集 tool_result
		results := make([]responses.ResponseInputItemUnionParam, 0, len(uses))
		for _, use := range uses {
			emit(Event{Kind: EventToolCall, Name: use.name, Text: use.arguments})
			out := a.runTool(ctx, use)
			emit(Event{Kind: EventToolResult, Name: use.name, Text: out})
			results = append(results, toolResult(use.callID, out))
		}

		// 5. tool_result 写回上下文，进入下一轮
		a.session.AddMessage(results...)
	}
}

// chat 调一次模型：把 messages 与 tools 一起发出去
func (a *Agent) chat(ctx context.Context) (*responses.Response, error) {
	return a.client.Responses.New(ctx, responses.ResponseNewParams{
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: a.session.GetMessages(),
		},
		Tools: a.openaiTools,
		Model: a.model,
	})
}

// toolUses 取本轮所有 tool_use（function_call）
func toolUses(resp *responses.Response) []toolUse {
	uses := make([]toolUse, 0)
	for _, item := range resp.Output {
		if item.Type == "function_call" {
			call := item.AsFunctionCall()
			uses = append(uses, toolUse{
				callID:    call.CallID,
				name:      call.Name,
				arguments: call.Arguments,
			})
		}
	}
	return uses
}

// reasoningText 汇总模型的思考摘要
func reasoningText(resp *responses.Response) string {
	var b strings.Builder
	for _, item := range resp.Output {
		if item.Type != "reasoning" {
			continue
		}
		for _, summary := range item.Summary {
			b.WriteString(summary.Text)
		}
	}
	return b.String()
}

// appendAssistant 把 assistant 的工具调用写回上下文
func (a *Agent) appendAssistant(uses []toolUse) {
	for _, use := range uses {
		a.session.AddMessage(responses.ResponseInputItemUnionParam{
			OfFunctionCall: &responses.ResponseFunctionToolCallParam{
				Arguments: use.arguments,
				CallID:    use.callID,
				Name:      use.name,
			},
		})
	}
}

// runTool 执行单个工具，出错时把错误文本作为结果返回给模型
func (a *Agent) runTool(ctx context.Context, use toolUse) string {
	for _, t := range a.tools {
		if t.Name != use.name {
			continue
		}
		out, err := t.Call(ctx, use.arguments)
		if err != nil {
			return fmt.Sprintf("%s 执行失败: %v", use.name, err)
		}
		return out
	}
	return fmt.Sprintf("未知工具: %s", use.name)
}

// toolResult 把工具结果包装成可回填上下文的 tool_result
func toolResult(callID, out string) responses.ResponseInputItemUnionParam {
	return responses.ResponseInputItemUnionParam{
		OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
			Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{
				OfString: openai.String(out),
			},
			CallID: openai.String(callID),
		},
	}
}

// DescribeError 把错误转成便于阅读的文本（含 HTTP 状态码与响应体）
func DescribeError(err error) string {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		return fmt.Sprintf("HTTP %d %s: %s", apiErr.StatusCode, apiErr.Type, apiErr.RawJSON())
	}
	return err.Error()
}
