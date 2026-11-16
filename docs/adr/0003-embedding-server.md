# 0003 埋め込みをTEIの別コンテナで計算する

状態 承認

## 状況

アプリケーションはGoで書きます。日本語の埋め込みモデルRuri v3はsafetensorsとSentencePieceのトークナイザで配布されていて、ONNXの形式では配布されていません。Goだけで動かすにはONNXへの変換とGo側のトークナイザの実装が必要になります。

## 決定

埋め込みは、Hugging Faceの[text-embeddings-inference](https://github.com/huggingface/text-embeddings-inference)（TEI）で計算し、Goとは別のコンテナで動かします。TEIはRuri v3が使うModernBERTの構造に対応しています。モデルのファイルはコミットを指定してイメージに同梱し、起動時にHugging Faceから取得しない形にします。

## 影響

- TEIで計算した埋め込みは、30m、130m、310mのいずれもsentence-transformersで計算したものと一致しました（同じ文60件でコサイン類似度の最小値1.00000）。
- TEIのイメージはarm64版を使い、Gravitonで動かします。
- TEIは起動時の準備で、1回にまとめて処理する最大のトークン数（`MAX_BATCH_TOKENS`、既定16,384）で試しに計算します。既定のままでは130mと310mが8GiBのメモリを超えて強制終了したので、4,096にしています。このとき準備中の最大メモリは、30mで1.88GiB、130mで3.90GiB、310mで5.79GiBでした。コンテナのメモリはこの値をもとに決めます。
- 並べ替えのモデル`ruri-v3-reranker-310m`もTEIで動き、順位はsentence-transformersのCrossEncoderと一致しました。ただしCPUでは遅すぎたので、並べ替えには使いません（[0012](0012-ranking.md)）。
