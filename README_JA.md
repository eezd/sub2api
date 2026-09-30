<div align="center">

<img src="assets/logo.svg" alt="Sub2API Logo" width="128" />

# Sub2API — eezd fork

[![Release](https://img.shields.io/github/v/release/eezd/sub2api?include_prereleases&label=release)](https://github.com/eezd/sub2api/releases)
[![Go](https://img.shields.io/badge/Go-1.27.0-00ADD8.svg)](https://go.dev/)
[![Vue](https://img.shields.io/badge/Vue-3.4+-4FC08D.svg)](https://vuejs.org/)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-15+-336791.svg)](https://www.postgresql.org/)
[![Redis](https://img.shields.io/badge/Redis-7+-DC382D.svg)](https://redis.io/)

**サブスクリプションクォータの配分、マルチプロバイダールーティング、運用管理に対応した AI API ゲートウェイ。**

[English](README.md) · [中文](README_CN.md) · 日本語

</div>

> 本リポジトリは [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api) の `eezd/sub2api` fork です。上流を同期しながら、以下に記載する fork 固有の機能を追加しています。上流の公式リリースではありません。

## この fork の追加機能

以下は上流版との主な違いです。Sub2API のコア機能は引き続き利用できます。ここでは、この fork で保守している機能を明示します。

| Fork の追加機能 | 内容 | ドキュメント |
|---|---|---|
| **Infinite Canvas ワークスペース** | `/workspace/canvas` に Infinite Canvas を統合し、`INFINITE_CANVAS_URL` による外部デプロイにも対応します。ローカル Codex Agent に接続できます。API Key を URL に埋め込みません。 | [`docs/infinite-canvas.md`](docs/infinite-canvas.md) |
| **アカウントの最近のリクエスト履歴** | 管理画面のアカウント一覧に「Recent Requests」列を追加します。直近 15 分間の最大 5 件について、成功/エラー、レイテンシ、First Token、Token 数、コスト、倍率、詳細を表示します。アカウントごとの N+1 ポーリングを避け、ページ単位のバッチ取得を行います。 | [`POST /api/v1/admin/ops/requests/recent-by-account`](backend/internal/server/routes/admin.go) |
| **ModelTrace モデル劣化チェック** | OpenAI / Anthropic のテキストモデルアカウント向けに、管理者専用の検査を追加します。管理者がモデルを明示的に選択し、サーバーは最大 6 回の呼び出しで有効な意味なし数列を 3 件収集して、同梱の GPT/Claude 参照バンクに対する閉集合統計帰属を行います。出力上限・拒否・コンテンツフィルタで正常終了しなかった回答は採用しません。成功・失敗・キャンセルの履歴はアカウント単位で永続化されます。結果は診断用であり、モデル同一性の確定証明ではありません。 | [`POST /api/v1/admin/accounts/:id/model-trace`](backend/internal/server/routes/admin.go)<br>[履歴 API](backend/internal/server/routes/admin.go) |
| **SVG アニメーション劣化チェック** | ModelTrace の数値フィンガープリント課題とは別に、1 回呼び出しの視覚テストを追加します。管理者がテキストモデルを選択すると、固定のペリカン自転車 HTML/SVG プロンプトを送信し、スクリプトを無効化した分離サンドボックスで結果を表示します。CSP はモデル出力より前に固定配置され、プレビューは外部リソースを読み込めません。切り詰め・フィルタされた回答は失敗として記録します。成功・失敗・キャンセルの履歴を永続化し、過去のアニメーションを再生できます。 | [`POST /api/v1/admin/accounts/:id/svg-animation-test`](backend/internal/server/routes/admin.go)<br>[履歴 API](backend/internal/server/routes/admin.go) |
| **永続的な一括劣化チェック** | ページをまたいでアカウントを選択し、一括操作バーの **Degradation Check** または **SVG Animation Degradation Check** を実行します（最大 1,000 件）。モデルはアカウントごとに明示的に選択し、プラットフォーム単位の適用はそのモデルを利用できるアカウントだけに行います。非対応・未選択の行はスキップします。サーバー側タスクはダイアログ終了、画面移動、再読込、ログアウト後も継続し、選択なしで **Bulk check tasks** から再確認できます。明示的なキャンセルは待機中・実行中の項目を停止し、完了済み結果とアカウント履歴を保持します。同じデータベースを共有する複数インスタンス全体で同時実行は最大 2 件、同一アカウントは直列です。中断項目は自動再実行せず、未実行項目を続行します。検査は上流の利用枠を消費します。 | [`/api/v1/admin/accounts/degradation-check-batches`](backend/internal/server/routes/admin.go) |
| **Codex turn-state チケット** | 対応する OpenAI OAuth モデル向けに `x-codex-turn-state` を任意で取得・注入できます。管理画面の有効化、取得プロキシ、TTL、モデル範囲、フェイルクローズを設定できます。 | [`deploy/config.example.yaml`](deploy/config.example.yaml) の `gateway.openai_codex_ticket` |
| **署名付き OpenAI Transport プラグイン** | OpenAI OAuth の出力 HTTP/TLS トランスポート用公式 `.s2plugin` を提供します。Node.js 24 形式の ClientHello、アカウントプロキシ継承、接続制限、タイムアウト、UI 設定、署名検証、段階的ロールアウトに対応します。 | [`docs/PLUGIN_INSTALLATION.md`](docs/PLUGIN_INSTALLATION.md) |
| **TLS フィンガープリントプロファイル** | 管理対象の TLS フィンガープリントを提供します。ClientHello フィールドを制限し、変更操作にはステップアップ認証を要求します。直接接続、HTTP CONNECT、SOCKS5H プロキシに対応します。 | [`docs/PLUGIN_DEVELOPMENT.md`](docs/PLUGIN_DEVELOPMENT.md)、`backend/pkg/pluginapi/docs/` |
| **強化された出力 URL セキュリティ** | 新規インストールでは HTTPS による出力先検証を有効にし、プライベート、ループバック、リンクローカル、未指定アドレスを拒否します。リダイレクトとレスポンス由来 URL も個別に検証します。 | [`deploy/EDGE_SECURITY.md`](deploy/EDGE_SECURITY.md) |
| **Fork のリリースライン** | `vX.Y.Z-custom.N` 形式のリリース、署名済みプラグイン、完全なバージョンタグを持つ GHCR イメージを公開します。現在のリリースは [`v0.2.9-custom.1`](https://github.com/eezd/sub2api/releases/tag/v0.2.9-custom.1) です。 | [Release workflow](.github/workflows/release.yml) |

### 公式プラグインの互換性

公式プラグインは、テスト済みの fork 版と同じ Release で公開されます。

```text
Sub2API: 0.2.9-custom.1
Plugin:  0.2.9-custom.1
Requires: >=0.2.9 <0.3.0
```

同じ GitHub Release から対応する `.s2plugin` をダウンロードしてください。本番環境では `plugins.allow_unsigned: false` を維持してください。公式パッケージはホスト内蔵の公開鍵で検証されます。

## 継承される上流機能

この fork には、以下を含む上流プラットフォームの機能が引き続き含まれます。

- OAuth および API Key アカウントのマルチアカウント管理。
- API Key 配布、グループ、スティッキーセッション、アカウントスケジューリング、フェイルオーバー、同時実行数制限、レート制限。
- Token 単位の使用量、モデル価格、課金、サブスクリプション、組み込み決済プロバイダー。
- 現行リリースで利用できる OpenAI 互換、Anthropic 互換、Gemini、Grok/xAI、Antigravity などのプロバイダー連携。
- Composite Groups によるモデルベースのプロバイダールーティング（[運用ガイド](docs/COMPOSITE_GROUPS.md)）。
- 同期/非同期画像タスク、バッチ画像処理、OpenAI Responses WebSocket の ingress 制御。
- 管理監視、使用量レポート、バックアップ、Prompt Audit、セキュリティ設定、外部管理画面連携。
- 個人または内部チーム向けの Simple Mode。

上流の履歴と基本アーキテクチャについては [上流プロジェクト](https://github.com/Wei-Shaw/sub2api) を参照してください。Fork 固有の動作は本 README とリンク先のドキュメントを正とします。

## 重要なお知らせ

- OAuth アカウント、サブスクリプションクォータ、API リレー機能を使用する前に、すべての上流プロバイダーの利用規約を確認してください。
- 適用される法律、プロバイダー契約、プライバシー要件、決済ルール、ネットワークアクセス制限を遵守して利用してください。
- 本ソフトウェアは技術学習・研究目的で提供されます。アカウント、データ、ユーザー、課金、コンテンツ、コンプライアンス、運用セキュリティについてはデプロイ者が責任を負います。
- 本リポジトリに基づくデプロイ、ホスティング、料金プラン、リレーサービス、商用活動を本プロジェクトが許可・推奨するものではありません。公開または商用インスタンスを運用する前に [`docs/legal/admin-compliance.en.md`](docs/legal/admin-compliance.en.md) を確認してください。

## クイックスタート

### Docker Compose

Docker Compose は開発およびセルフホスティングに適しています。リポジトリ内の Compose ファイルはデフォルトで上流イメージ名を参照します。この fork を使用する場合は、fork の Release に対応する GHCR イメージへ置き換えてください。

```bash
git clone https://github.com/eezd/sub2api.git
cd sub2api/deploy
cp .env.example .env
chmod 600 .env

# .env に強力な POSTGRES_PASSWORD、JWT_SECRET、TOTP_ENCRYPTION_KEY を設定します。
# fork の固定バージョンへ置き換えます。
sed -i 's#weishaw/sub2api:latest#ghcr.io/eezd/sub2api:0.2.9-custom.1#' docker-compose.local.yml

mkdir -p data postgres_data redis_data
docker compose -f docker-compose.local.yml up -d
docker compose -f docker-compose.local.yml logs -f sub2api
```

`http://YOUR_SERVER_IP:8080` を開いてください。`AUTO_SETUP=true` の場合、コンテナはマイグレーションを適用し、初期管理者を作成します。`ADMIN_PASSWORD` を設定していない場合は、アプリケーションログから自動生成されたパスワードを確認します。

本番環境では `latest` を使わず、fork の固定バージョンを使用してください。ローカルディレクトリ版は `data/`、`postgres_data/`、`redis_data/` をまとめてバックアップ・移行できるため推奨です。

Compose の種類、復旧動作、Apple `container`、移行、運用コマンドについては [`deploy/README.md`](deploy/README.md) を参照してください。

### ビルド済みバイナリ

[fork Releases](https://github.com/eezd/sub2api/releases) から対応するアーカイブをダウンロードし、PostgreSQL 15+ と Redis 7+ を準備してください。設定ファイルがない状態で起動するとセットアップウィザードが表示され、最初の管理者を作成できます。

現在の Release は以下のターゲットを含みます。

- Linux amd64、arm64
- macOS amd64、arm64
- Windows amd64

### ソースからビルド

```bash
git clone https://github.com/eezd/sub2api.git
cd sub2api

corepack enable
cd frontend
pnpm install
pnpm run build

cd ../backend
VERSION="$(./scripts/resolve-version.sh)"
go build -tags embed -ldflags="-X main.Version=${VERSION}" -o sub2api ./cmd/server
./sub2api
```

`embed` ビルドタグはフロントエンドをバイナリへ埋め込みます。開発時にバックエンドとフロントエンドを別々に起動する場合は [`DEV_GUIDE.md`](DEV_GUIDE.md) を参照してください。

## 主要設定

### 安全な出力デフォルト

新規インストールでは出力 URL 境界を有効にしてください。

```yaml
security:
  url_allowlist:
    enabled: true
    upstream_hosts:
      - api.openai.com
      - api.anthropic.com
    allow_private_hosts: false
    allow_insecure_http: false
```

プロバイダーのホスト名を `upstream_hosts` に追加し、追加のために境界を無効化しないでください。クロスオリジンリダイレクトは認証情報転送前に拒否されます。レスポンス由来 URL は公開先に限定され、リダイレクトの各 hop が検証されます。

以下の弱い設定は、隔離された開発環境でのみ使用してください。

```yaml
security:
  url_allowlist:
    enabled: false
    allow_private_hosts: true
    allow_insecure_http: true
```

これらのチェックを無効にすると、認証情報や内部サービスが露出する可能性があります。互換性のために無効化する場合は、ネットワーク境界で同等の出力制御を実装してください。

### OpenAI WebSocket フォールバック

プロキシが OpenAI Responses WebSocket を繰り返し再接続する場合、クライアント側のプロトコルを変更せずに上流を HTTP/SSE へ切り替えられます。

```yaml
gateway:
  openai_ws:
    force_http: true
```

Compose では `GATEWAY_OPENAI_WS_FORCE_HTTP=true` を設定します。WebSocket の全設定は [`deploy/config.example.yaml`](deploy/config.example.yaml) にあります。

### Codex チケット取得

この機能はデフォルトで無効です。必要なアカウントとモデルに対してのみ有効化してください。

```yaml
gateway:
  openai_codex_ticket:
    enabled: true
    harvest_proxy_url: socks5h://user:password@proxy.example:1080
    models:
      - gpt-6-astra
      - gpt-5.6-sol
    fail_closed: true
```

チケットはシステム管理です。アカウント編集リクエストやユーザー指定のアカウントメタデータへチケット値を書き込まないでください。取得プロキシの認証情報は秘密情報として扱ってください。

## OpenAI Transport プラグインのインストール

1. 対応する fork Release から `sub2api-openai-transport-<version>.s2plugin` をダウンロードします。
2. 管理画面の **Plugins** を開き、パッケージをアップロードして署名と互換性を確認します。
3. 有効化する前に設定とテストを完了します。
4. 低いロールアウト率から開始し、プラグイン状態、成功率、レイテンシ、ログを監視します。
5. バインディングを無効化すると、OpenAI OAuth アカウントはコアトランスポートへ戻ります。

プラグインはアカウント選択、OAuth 更新、SSE 解析、リトライ、課金を置き換えません。署名済みパッケージを展開・変更しないでください。インストール、アップグレード、ロールバック、設定、ソースビルドの詳細は [`docs/PLUGIN_INSTALLATION.md`](docs/PLUGIN_INSTALLATION.md) を参照してください。

## Infinite Canvas

この fork は以下のパスに Canvas を統合しています。

```text
/workspace/canvas
```

デフォルトの同一オリジン構成では `/canvas-app/` から組み込みアプリを提供します。`INFINITE_CANVAS_URL` を設定すると外部 Canvas を利用できます。外部アプリは定義済み bridge を実装する必要があります。API Key は認証済み bridge 経由で渡され、公開 URL には含まれません。

Canvas ワークスペースから Codex Agent に接続するには次を実行します。

```bash
npx -y @basketikun/canvas-agent
```

デプロイ、bridge セキュリティ、外部ホスティング、トラブルシューティングは [`docs/infinite-canvas.md`](docs/infinite-canvas.md) を参照してください。

## 管理画面の最近のリクエスト

アカウント一覧で「Recent Requests」列を表示すると、現在のページを 1 回のバッチリクエストで更新します。各アカウントについて直近 15 分間の最大 5 件を表示し、リクエスト ID、モデル、状態、時刻、Token、コスト、アカウント倍率の詳細を開けます。

この機能は、旧来のアカウントごとのポーリングを意図的に避けています。現在表示されているアカウント ID を受け取り、グループ化された結果を返します。

```http
POST /api/v1/admin/ops/requests/recent-by-account
Content-Type: application/json

{"account_ids":[101,102,103]}
```

## Nginx とリバースプロキシ

Codex などアンダースコアを含むリクエストヘッダーを使用するクライアントを Nginx 経由で接続する場合、`http` ブロックに以下を追加します。

```nginx
underscores_in_headers on;
```

また、Sub2API に直接接続するプロキシ CIDR のみを `server.trusted_proxies` に設定してください。転送されたクライアント IP ヘッダーを信頼する前に [`deploy/EDGE_SECURITY.md`](deploy/EDGE_SECURITY.md) を確認してください。

## ドキュメント一覧

| トピック | ドキュメント |
|---|---|
| デプロイとアップグレード | [`deploy/README.md`](deploy/README.md) |
| 決済設定 | [`docs/PAYMENT.md`](docs/PAYMENT.md) |
| Composite Groups | [`docs/COMPOSITE_GROUPS.md`](docs/COMPOSITE_GROUPS.md) |
| 非同期画像タスク | [`docs/ASYNC_IMAGE_TASKS.md`](docs/ASYNC_IMAGE_TASKS.md) |
| バッチ画像処理 | [`docs/BATCH_IMAGE_MVP.md`](docs/BATCH_IMAGE_MVP.md) |
| OpenAI Transport インストール | [`docs/PLUGIN_INSTALLATION.md`](docs/PLUGIN_INSTALLATION.md) |
| プラグイン開発とパッケージ形式 | [`docs/PLUGIN_DEVELOPMENT.md`](docs/PLUGIN_DEVELOPMENT.md)、[`backend/pkg/pluginapi/docs/`](backend/pkg/pluginapi/docs/) |
| Infinite Canvas | [`docs/infinite-canvas.md`](docs/infinite-canvas.md) |
| エッジとプロキシのセキュリティ | [`deploy/EDGE_SECURITY.md`](deploy/EDGE_SECURITY.md) |
| 開発ガイド | [`DEV_GUIDE.md`](DEV_GUIDE.md) |

## プロジェクト構成

```text
sub2api/
├── backend/                  # Go ゲートウェイ、サービス、リポジトリ、マイグレーション
├── frontend/                 # Vue 管理画面とユーザー画面
├── plugins/openai-transport/ # 公式 OpenAI Transport プラグイン UI/ランタイム
├── integrations/infinite-canvas/ # Canvas サブモジュール
├── deploy/                   # Compose、バイナリ、Apple container、設定
├── docs/                     # 運用、セキュリティ、決済、プラグインのドキュメント
└── scripts/                  # ビルド・統合用ヘルパー
```

## ライセンス

本プロジェクトは [GNU Lesser General Public License v3.0 またはそれ以降](LICENSE) の下で公開されています。

Fork は上流のライセンスと帰属表示を維持します。Fork 固有のコードとドキュメントは、リポジトリに適用されるライセンスに従います。詳細は [`LICENSE`](LICENSE) と [`CLA.md`](CLA.md) を参照してください。

<div align="center">

**この fork が役立った場合は、Star と再現可能な Issue の報告をお願いします。**

</div>
