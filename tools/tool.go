package tools

import (
	"context"
	"encoding/json"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
)

type ToolDef struct {
	Name        string                                                         //工具名称
	Description string                                                         //工具描述
	Params      openai.FunctionParameters                                      //工具参数
	Fn          func(ctx context.Context, args map[string]any) (string, error) //工具函数
}

func (t ToolDef) ToParam() responses.ToolUnionParam { //将工具定义转换为 OpenAI 工具参数
	return responses.ToolUnionParam{
		OfFunction: &responses.FunctionToolParam{
			Name:        t.Name,
			Description: openai.String(t.Description),
			Parameters:  t.Params,
		},
	}
}

func (t ToolDef) Call(ctx context.Context, argsJSON string) (string, error) { //调用工具函数
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	return t.Fn(ctx, args)
}
