# 0002 RDS for PostgreSQLでpgvectorとpg_bigmを使う

状態 承認

## 状況

意味の近さで探す検索と文字列の部分一致で探す検索を、1つのDBで行いたいと考えました。DBの運用は自分で行わず、AWSのマネージドサービスに任せる方針です。

## 決定

Amazon RDS for PostgreSQL 18.6を使い、[RDSで使える拡張](https://docs.aws.amazon.com/AmazonRDS/latest/PostgreSQLReleaseNotes/postgresql-extensions.html)のうちpgvector 0.8.2とpg_bigm 1.2_20250903を入れます。

## 検討した選択肢

- PGroonga RDSの拡張一覧に含まれていないので、使うには自分でPostgreSQLを運用する必要があります。
- pg_trgm RDSで使えますが、日本語の表記ゆれの計測では、別の表記の文書を拾う力がベクトル検索より低い結果でした。
- 専用の検索エンジン（OpenSearchなど） DBと検索エンジンを二重に管理することになります。

## 影響

- 既定の検索はベクトル検索です（[0012](0012-ranking.md)）。pg_bigmは、比較のための部分一致の検索で使います。部分一致の検索はpg_bigmの索引を使った`LIKE`で行います。並び順の手がかりは一致の有無と`bigm_similarity`だけで、BM25のような関連度の点数はありません。
- 形態素解析を使わないので、文の形の検索語は部分一致ではほとんど見つかりません。文の形の検索語はベクトル検索で扱います。
