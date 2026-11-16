package rerank

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockagentruntime/types"
)

type fakeAPI struct {
	in  *bedrockagentruntime.RerankInput
	out *bedrockagentruntime.RerankOutput
	err error
}

func (f *fakeAPI) Rerank(_ context.Context, in *bedrockagentruntime.RerankInput, _ ...func(*bedrockagentruntime.Options)) (*bedrockagentruntime.RerankOutput, error) {
	f.in = in
	return f.out, f.err
}

func results(idx ...int32) *bedrockagentruntime.RerankOutput {
	out := &bedrockagentruntime.RerankOutput{}
	for i, x := range idx {
		out.Results = append(out.Results, types.RerankResult{Index: aws.Int32(x), RelevanceScore: aws.Float32(1 / float32(i+1))})
	}
	return out
}

const arn = "arn:aws:bedrock:ap-northeast-1::foundation-model/cohere.rerank-v3-5:0"

func TestRerankReturnsIndicesInResultOrder(t *testing.T) {
	api := &fakeAPI{out: results(2, 0, 1)}
	got, err := (&Client{API: api, ModelARN: arn, TopN: 3}).Rerank(context.Background(), "q", []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != 2 || got[1] != 0 || got[2] != 1 {
		t.Fatalf("got %v, want [2 0 1]", got)
	}
}

func TestRerankSendsModelARNTopNAndTexts(t *testing.T) {
	api := &fakeAPI{out: results(0)}
	if _, err := (&Client{API: api, ModelARN: arn, TopN: 10}).Rerank(context.Background(), "パスワード", []string{"本文"}); err != nil {
		t.Fatal(err)
	}
	cfg := api.in.RerankingConfiguration
	if cfg.Type != types.RerankingConfigurationTypeBedrockRerankingModel ||
		aws.ToString(cfg.BedrockRerankingConfiguration.ModelConfiguration.ModelArn) != arn ||
		aws.ToInt32(cfg.BedrockRerankingConfiguration.NumberOfResults) != 1 {
		t.Fatalf("config = %+v, want the ARN and NumberOfResults capped at the 1 text", cfg)
	}
	if aws.ToString(api.in.Queries[0].TextQuery.Text) != "パスワード" || aws.ToString(api.in.Sources[0].InlineDocumentSource.TextDocument.Text) != "本文" {
		t.Fatalf("query or source text not sent: %+v", api.in)
	}
}

func TestRerankWithoutTopNReturnsEveryText(t *testing.T) {
	api := &fakeAPI{out: results(1, 0)}
	if _, err := (&Client{API: api, ModelARN: arn}).Rerank(context.Background(), "q", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if n := aws.ToInt32(api.in.RerankingConfiguration.BedrockRerankingConfiguration.NumberOfResults); n != 2 {
		t.Fatalf("NumberOfResults = %d, want 2", n)
	}
}

func TestRerankCapsNumberOfResultsAtTopN(t *testing.T) {
	api := &fakeAPI{out: results(1)}
	if _, err := (&Client{API: api, ModelARN: arn, TopN: 1}).Rerank(context.Background(), "q", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if n := aws.ToInt32(api.in.RerankingConfiguration.BedrockRerankingConfiguration.NumberOfResults); n != 1 {
		t.Fatalf("NumberOfResults = %d, want 1", n)
	}
}

func TestRerankRejectsTooManyTexts(t *testing.T) {
	api := &fakeAPI{}
	_, err := (&Client{API: api, ModelARN: arn}).Rerank(context.Background(), "q", make([]string, MaxSources+1))
	if err == nil || api.in != nil {
		t.Fatalf("err = %v, called = %v; want an error before calling Bedrock", err, api.in != nil)
	}
	api = &fakeAPI{out: results(0)}
	if _, err := (&Client{API: api, ModelARN: arn, TopN: 1}).Rerank(context.Background(), "q", make([]string, MaxSources)); err != nil {
		t.Fatalf("exactly MaxSources texts must be accepted: %v", err)
	}
}

func TestRerankEmptyTextsMakesNoCall(t *testing.T) {
	api := &fakeAPI{}
	got, err := (&Client{API: api, ModelARN: arn, TopN: 10}).Rerank(context.Background(), "q", nil)
	if err != nil || len(got) != 0 || api.in != nil {
		t.Fatalf("got %v, %v, called = %v", got, err, api.in != nil)
	}
}

func TestRerankReturnsErrorOnThrottling(t *testing.T) {
	api := &fakeAPI{err: &types.ThrottlingException{Message: aws.String("Your request rate is too high.")}}
	_, err := (&Client{API: api, ModelARN: arn, TopN: 10}).Rerank(context.Background(), "q", []string{"a"})
	var te *types.ThrottlingException
	if !errors.As(err, &te) {
		t.Fatalf("err = %v, want it to wrap ThrottlingException", err)
	}
}

func TestRerankRejectsOutOfRangeIndex(t *testing.T) {
	for _, bad := range []int32{1, -1} { // 1 is the first index past the end of one text
		api := &fakeAPI{out: results(bad)}
		_, err := (&Client{API: api, ModelARN: arn, TopN: 10}).Rerank(context.Background(), "q", []string{"a"})
		if err == nil || !strings.Contains(err.Error(), "out of range") {
			t.Errorf("index %d: err = %v", bad, err)
		}
	}
}
