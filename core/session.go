package core

import (
	"github.com/openai/openai-go/v3/responses"
)

type Session struct {
	Messages responses.ResponseInputParam //按顺序存储对话的消息
	Len      int                          //记录当前已存储的消息数量
}

func NewSession() *Session { //创建一个新的会话
	return &Session{
		Messages: responses.ResponseInputParam{},
		Len:      0,
	}
}

func (s *Session) GetMessages() responses.ResponseInputParam { //获取会话中的所有消息
	return s.Messages
}

func (s *Session) GetLen() int { //获取会话中的消息数量
	return s.Len
}

func (s *Session) AddMessage(item ...responses.ResponseInputItemUnionParam) { //添加消息到会话
	s.Messages = append(s.Messages, item...)
	s.Len++
}
