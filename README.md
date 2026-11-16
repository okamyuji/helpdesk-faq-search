# helpdesk-faq-search

社内ヘルプデスク向けのFAQ検索を、pgvector、Ruri v3、Amazon BedrockのCohere Rerank 3.5で組み立てる検証用の実装です。

| ディレクトリ | 役割 |
|---|---|
| `cmd/ics` | `serve`（検索画面）、`load`（取り込み）、`eval`（評価）のコマンド。画面に認証はありません |
| `internal/ingest` | JaGovFaqs-22kのJSONLを読む |
| `internal/textnorm` | NFKCの正規化 |
| `internal/embed` | TEIで埋め込みを計算する |
| `internal/rerank` | Bedrockで候補を並べ替える |
| `internal/store` | PostgreSQLの表と索引、読み書き |
| `internal/search` | ベクトル検索、並べ替え、キーワード検索、RRF |
| `internal/load` | FAQと埋め込みを取り込む |
| `internal/eval` | 言い換えた問い合わせで検索の質を測る |
| `internal/web` | 検索画面 |
| `eval` | 評価に使う言い換えた問い合わせ |
| `docker/tei` | Ruri v3を同梱したTEIのイメージ |
| `testdata/pg` | 試験用のPostgreSQLのイメージ（pgvectorとpg_bigm） |
| `infra` | AWSの構成（Terraform） |
| `scripts` | TEIの一致の確認と、Claude APIでHaikuを測るスクリプト |
| `docs/adr` | 設計の判断の記録 |

データはJaGovFaqs-22k（CC BY 4.0）、埋め込みモデルRuri v3はApache 2.0です。
