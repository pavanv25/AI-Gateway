package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/pavanv25/ai-gateway/pkg/models"
)

const anthropicDefaultMaxTokens = 1024

type AnthropicProvider struct {
	client *anthropic.Client
}

func NewAnthropicProvider(apiKey string) *AnthropicProvider {
	c := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &AnthropicProvider{client: &c}
}

func (p *AnthropicProvider) Name() string { return "anthropic" }

// buildAnthropicMessages splits system messages out of the conversation,
// as Anthropic's API expects them in a separate top-level field.
func buildAnthropicMessages(msgs []models.Message) (params []anthropic.MessageParam, systemText string) {
	var sysParts []string
	for _, m := range msgs {
		switch m.Role {
		case "system":
			sysParts = append(sysParts, m.Content)
		case "assistant":
			params = append(params, anthropic.NewAssistantMessage(anthropic.NewTextBlock(m.Content)))
		default:
			params = append(params, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		}
	}
	return params, strings.Join(sysParts, "\n")
}

func (p *AnthropicProvider) Chat(ctx context.Context, req *models.ChatRequest) (*models.ChatResponse, error) {
	msgs, systemText := buildAnthropicMessages(req.Messages)

	maxTok := int64(req.MaxTokens)
	if maxTok <= 0 {
		maxTok = anthropicDefaultMaxTokens
	}

	msgParams := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: maxTok,
		Messages:  msgs,
	}
	if systemText != "" {
		msgParams.System = []anthropic.TextBlockParam{{Text: systemText}}
	}
	if req.Temperature != nil {
		msgParams.Temperature = param.NewOpt(*req.Temperature)
	}

	msg, err := p.client.Messages.New(ctx, msgParams)
	if err != nil {
		return nil, fmt.Errorf("anthropic chat: %w", err)
	}

	var sb strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}

	return &models.ChatResponse{
		ID:    msg.ID,
		Model: string(msg.Model),
		Choices: []models.Choice{{
			Index:        0,
			Message:      models.Message{Role: "assistant", Content: sb.String()},
			FinishReason: string(msg.StopReason),
		}},
		Usage: models.Usage{
			PromptTokens:     int(msg.Usage.InputTokens),
			CompletionTokens: int(msg.Usage.OutputTokens),
			TotalTokens:      int(msg.Usage.InputTokens + msg.Usage.OutputTokens),
		},
	}, nil
}

func (p *AnthropicProvider) ChatStream(ctx context.Context, req *models.ChatRequest) (<-chan models.StreamEvent, error) {
	msgs, systemText := buildAnthropicMessages(req.Messages)

	maxTok := int64(req.MaxTokens)
	if maxTok <= 0 {
		maxTok = anthropicDefaultMaxTokens
	}

	msgParams := anthropic.MessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: maxTok,
		Messages:  msgs,
	}
	if systemText != "" {
		msgParams.System = []anthropic.TextBlockParam{{Text: systemText}}
	}
	if req.Temperature != nil {
		msgParams.Temperature = param.NewOpt(*req.Temperature)
	}

	stream := p.client.Messages.NewStreaming(ctx, msgParams)

	ch := make(chan models.StreamEvent, 32)
	go func() {
		defer close(ch)

		var eventID string
		var inputTokens int
		var sentDone bool

		for stream.Next() {
			switch ev := stream.Current().AsAny().(type) {
			case anthropic.MessageStartEvent:
				eventID = ev.Message.ID
				inputTokens = int(ev.Message.Usage.InputTokens)
			case anthropic.ContentBlockDeltaEvent:
				if delta, ok := ev.Delta.AsAny().(anthropic.TextDelta); ok && delta.Text != "" {
					ch <- models.StreamEvent{ID: eventID, Delta: delta.Text}
				}
			case anthropic.MessageDeltaEvent:
				outputTokens := int(ev.Usage.OutputTokens)
				ch <- models.StreamEvent{
					ID:   eventID,
					Done: true,
					Usage: &models.Usage{
						PromptTokens:     inputTokens,
						CompletionTokens: outputTokens,
						TotalTokens:      inputTokens + outputTokens,
					},
				}
				sentDone = true
			}
		}
		if !sentDone {
			ch <- models.StreamEvent{Done: true}
		}
	}()
	return ch, nil
}
