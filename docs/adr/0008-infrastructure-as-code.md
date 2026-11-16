# 0008 インフラをTerraformで管理する

状態 承認

## 状況

検証環境は作っては消すことを繰り返します。手で作ると消し忘れが起き、同じ環境を作り直す手間もかかります。

## 決定

Terraform 1.17.2とAWS provider 6.73.0で管理します。DBのパスワードはRDSが管理するSecrets Managerのシークレットを使い、Terraformの状態ファイルに残さない形にします。全リソースに`Project=helpdesk-faq-search`のタグを付けます。

## 影響

- `terraform destroy`で検証環境を一度に消せます。
- 最終スナップショットを残さずに消すには、先に`terraform apply -var skip_final_snapshot=true`で状態に反映してから`destroy`します。`destroy`は状態に保存された値で削除するので、`destroy`にだけ変数を渡しても最終スナップショットが残ります。
- `destroy`のあとも、登録を解除したECSのタスク定義が`INACTIVE`として残ります。`aws ecs delete-task-definitions`で消します。
- Node.jsの実行環境は要りません。
