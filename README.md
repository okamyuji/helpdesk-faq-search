# helpdesk-faq-search

社内ヘルプデスク向けのFAQ検索を、PostgreSQLのpgvectorと日本語の埋め込みモデルで作る検証用の実装です。
利用者の言い方で書かれた問い合わせから、該当するFAQの回答を上位に出すことを目的にしています。

## 構成

- 検索 Ruri v3の埋め込みでベクトル検索して30件を集め、Amazon BedrockのCohere Rerank 3.5で並べ替えて上位を表示します。
- 比較用の方式 ベクトル検索だけ、pg_bigmのキーワード検索、両者をRRFで混ぜる方式も画面で選べます。
- 埋め込み Hugging FaceのText Embeddings Inference（TEI）のコンテナでRuri v3を動かします。
- データベース Amazon RDS for PostgreSQLに、pgvectorとpg_bigmの拡張を入れて使います。
- 実行基盤 ECS Fargateの1つのタスクにアプリとTEIを同居させます。画面はECS Execのポート転送で開き、受信ポートは開けません。

判断の理由は[docs/adr](docs/adr/README.md)にあります。

## データ

[JaGovFaqs-22k](https://huggingface.co/datasets/matsuxr/JaGovFaqs-22k)（CC BY 4.0）を使います。
取り込むときはコミットのSHAで版を指定したURLから直接読み込みます。

## コマンド

`ics`は1つのバイナリで3つの役割を持ちます。

| コマンド | 役割 | 必須の環境変数 |
|---|---|---|
| `ics serve` | 検索画面を出す | `DATABASE_URL`, `TEI_EMBED_URL`, `RERANK_MODEL_ARN` |
| `ics load` | FAQを読み込み、埋め込みを計算して保存する | `DATABASE_URL`, `TEI_EMBED_URL` |
| `ics eval` | 言い換えた問い合わせで検索の質を測る | `DATABASE_URL`, `TEI_EMBED_URL`, `RERANK_MODEL_ARN` |

任意の環境変数は`EMBEDDING_MODEL`（既定`ruri-v3-30m`）、`LISTEN_ADDR`（既定`:8080`）、`AWS_REGION`、`HELPDESK_URL`、`DATA_SOURCE`、`EVAL_PARAPHRASES`です。

画面には認証がありません。手元で`ics serve`を動かすときは`LISTEN_ADDR=127.0.0.1:8080`のように手元だけで待ち受けてください。

## 開発

GoとDockerが必要です。

```sh
make check        # 整形、vet、lint、試験（試験用のPostgreSQLイメージも作る）
make tei-parity MODEL=ruri-v3-30m   # TEIの埋め込みがsentence-transformersと一致するか確かめる
make app-image    # アプリのイメージを作る
```

DBを使う試験はtestcontainersでPostgreSQLを起動します。
Colimaを使う場合は、`make`がColimaのソケットを`DOCKER_HOST`に設定します。
`go test`を直接実行するときは、同じ環境変数を自分で設定してください。

## AWSへの配置

`infra/`のTerraformで作ります。

```sh
cd infra
terraform init
terraform apply -var 'budget_emails=["you@example.com"]'
terraform destroy -var skip_final_snapshot=true   # 最終スナップショットを残さずに消す
```

予算の通知は、`budget_emails`に宛先を渡したときだけ作られます。

RDSには2つのアベイラビリティゾーンのサブネットが要ります。
既定のVPCに足りないときのために、`db_second_az`と`db_second_az_cidr`でサブネットを1つ足します。
自分のアカウントのVPCに合わせて値を変えてください。

## 計測用のスクリプト

`scripts/haiku_eval.py`は、候補の中から該当するFAQをClaude Haikuに選ばせる計測です。
AnthropicのClaude API（モデル`claude-haiku-5-5`）を使い、Amazon Bedrockは使いません。
APIキーは`ANTHROPIC_API_KEY_FILE`で指定したファイルから読みます。

## データとモデルのライセンス

データはJaGovFaqs-22kのCC BY 4.0、埋め込みモデルRuri v3はApache 2.0です。
