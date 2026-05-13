package provider

import (
	"context"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/pavanv25/ai-gateway/pkg/models"
)

type OpenAIProvider struct {
	client *openai.Client
}

func NewOpenAIProvider(apiKey string) *OpenAIProvider {
	return &OpenAIProvider{
		client: openai.NewClient(option.WithAPIKey(apiKey)),
	}
}

func (p *OpenAIProvider) Name() string { return "openai" }

func buildOpenAIMessages(msgs []models.Message) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, len(msgs))
	for i, m := range msgs {
		switch m.Role {
		case "system":
			out[i] = openai.SystemMessage(m.Content)
		case "assistant":
			out[i] = openai.AssistantMessage(m.Content)
		default:
			out[i] = openai.UserMessage(m.Content)
		}
	}
	return out
}

func (p *OpenAIProvider) Chat(ctx context.Context, req *models.ChatRequest) (*models.ChatResponse, error) {
	params := openai.ChatCompletionNewParams{
		Model:    openai.F(openai.ChatModel(req.Model)),
		Messages: openai.F(buildOpenAIMessages(req.Messages)),
	}
	if req.MaxTokens > 0 {
		params.MaxCompletionTokens = openai.Int(int64(req.MaxTokens))
	}
	if req.Temperature != nil {
		params.Temperature = openai.Float(*req.Temperature)
	}

	completion, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("openai chat: %w", err)
	}

	choices := make([]models.Choice, len(completion.Choices))
	for i, c := range completion.Choices {
		choices[i] = models.Choice{
			Index:        int(c.Index),
			Message:      models.Message{Role: string(c.Message.Role), Content: c.Message.Content},
			FinishReason: string(c.FinishReason),
		}
	}
	return &models.ChatResponse{
		ID:    completion.ID,
		Model: completion.Model,
		Choices: choices,
		Usage: models.Usage{
			PromptTokens:     int(completion.Usage.PromptTokens),
			CompletionTokens: int(completion.Usage.CompletionTokens),
			TotalTokens:      int(completion.Usage.TotalTokens),
		},
	}, nil
}

func (p *OpenAIProvider) ChatStream(ctx context.Context, req *models.ChatRequest) (<-chan models.StreamEvent, error) {
	params := openai.ChatCompletionNewParams{
		Model:    openai.F(openai.ChatModel(req.Model)),
		Messages: openai.F(buildOpenAIMessages(req.Messages)),
		StreamOptions: openai.F(openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		}),
	}
	if req.MaxTokens > 0 {
		params.MaxCompletionTokens = openai.Int(int64(req.MaxTokens))
	}
	if req.Temperature != nil {
		params.Temperature = openai.Float(*req.Temperature)
	}

	stream := p.client.Chat.Completions.NewStreaming(ctx, params)

	ch := make(chan models.StreamEvent, 32)
	go func() {
		defer close(ch)
		defer stream.Close()

		var sentDone bool
		for stream.Next() {
			chunk := stream.Current()
			// Final chunk sent by OpenAI when IncludeUsage=true: empty Choices, non-zero Usage.
			if len(chunk.Choices) == 0 {
				if chunk.Usage.TotalTokens > 0 {
					ch <- models.StreamEvent{
						ID:   chunk.ID,
						Done: true,
						Usage: &models.Usage{
							PromptTokens:     int(chunk.Usage.PromptTokens),
							CompletionTokens: int(chunk.Usage.CompletionTokens),
							TotalTokens:      int(chunk.Usage.TotalTokens),
						},
					}
					sentDone = true
				}
				continue
			}
			if delta := chunk.Choices[0].Delta.Content; delta != "" {
				ch <- models.StreamEvent{ID: chunk.ID, Delta: delta}
			}
		}
		if !sentDone {
			ch <- models.StreamEvent{Done: true}
		}
	}()
	return ch, nil
}
