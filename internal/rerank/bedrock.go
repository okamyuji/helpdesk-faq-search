// Package rerank orders search candidates with a reranking model on Amazon Bedrock (ADR 0012).
package rerank

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/types"
)

// API is the part of the Bedrock agent runtime client that Client uses.
type API interface {
	Rerank(ctx context.Context, in *bedrockagentruntime.RerankInput, opts ...func(*bedrockagentruntime.Options)) (*bedrockagentruntime.RerankOutput, error)
}

// MaxSources is the most texts one Rerank call accepts (RerankSourcesList in the Bedrock API model).
const MaxSources = 1000

// Client reranks with one Bedrock model, for example Cohere Rerank 3.5.
type Client struct {
	API      API
	ModelARN string
	TopN     int // 0 or less returns every text
}

// Rerank returns indexes into texts, most relevant first, at most TopN of them.
func (c *Client) Rerank(ctx context.Context, query string, texts []string) ([]int, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if len(texts) > MaxSources {
		return nil, fmt.Errorf("bedrock rerank: %d texts, at most %d", len(texts), MaxSources)
	}
	n := len(texts)
	if c.TopN > 0 {
		n = min(c.TopN, n)
	}
	sources := make([]types.RerankSource, len(texts))
	for i, t := range texts {
		sources[i] = types.RerankSource{Type: types.RerankSourceTypeInline, InlineDocumentSource: &types.RerankDocument{
			Type: types.RerankDocumentTypeText, TextDocument: &types.RerankTextDocument{Text: aws.String(t)}}}
	}
	out, err := c.API.Rerank(ctx, &bedrockagentruntime.RerankInput{
		Queries: []types.RerankQuery{{Type: types.RerankQueryContentTypeText, TextQuery: &types.RerankTextDocument{Text: aws.String(query)}}},
		Sources: sources,
		RerankingConfiguration: &types.RerankingConfiguration{Type: types.RerankingConfigurationTypeBedrockRerankingModel,
			BedrockRerankingConfiguration: &types.BedrockRerankingConfiguration{
				NumberOfResults:    aws.Int32(int32(n)), //nolint:gosec // n <= MaxSources (1000)
				ModelConfiguration: &types.BedrockRerankingModelConfiguration{ModelArn: aws.String(c.ModelARN)}}},
	})
	if err != nil {
		return nil, fmt.Errorf("bedrock rerank: %w", err)
	}
	idx := make([]int, len(out.Results))
	for i, r := range out.Results {
		x := int(aws.ToInt32(r.Index))
		if x < 0 || x >= len(texts) {
			return nil, fmt.Errorf("bedrock rerank: index %d out of range for %d texts", x, len(texts))
		}
		idx[i] = x
	}
	return idx, nil
}
