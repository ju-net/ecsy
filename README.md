# ecsy - ECS Command Execute Utility

[![CI](https://github.com/ju-net/ecsy/workflows/CI/badge.svg)](https://github.com/ju-net/ecsy/actions)
[![Release](https://github.com/ju-net/ecsy/workflows/Release/badge.svg)](https://github.com/ju-net/ecsy/actions)
[![Go Report Card](https://goreportcard.com/badge/github.com/ju-net/ecsy)](https://goreportcard.com/report/github.com/ju-net/ecsy)

MFA認証対応のECS Command Executeを簡単に実行するためのCLIツールです。

## 特徴

- **AWS プロファイルの選択**: `~/.aws/config`から自動検出
- **MFA認証対応**:
  - MFAデバイスの自動検出・選択
  - アクセス拒否時の自動MFA認証
  - 設定ファイルからのMFAシリアル自動取得
- **インタラクティブな選択UI**:
  - ECSクラスタ一覧から選択
  - サービス一覧から選択  
  - 実行中のタスクのみ表示・選択
- **柔軟な実行方式**:
  - 完全インタラクティブモード
  - コマンドライン引数による直接指定
  - 混在モード（一部指定、一部選択）
- **クロスプラットフォーム対応**: macOS, Linux, Windows

## インストール

### ビルド済みバイナリ

[Releases](https://github.com/ju-net/ecsy/releases)ページから環境に合ったバイナリをダウンロードしてください。

#### 手動インストール

```bash
# macOS (Intel)
curl -L https://github.com/ju-net/ecsy/releases/latest/download/ecsy-darwin-amd64.gz | gunzip > ecsy
chmod +x ecsy && sudo mv ecsy /usr/local/bin/

# macOS (Apple Silicon)  
curl -L https://github.com/ju-net/ecsy/releases/latest/download/ecsy-darwin-arm64.gz | gunzip > ecsy
chmod +x ecsy && sudo mv ecsy /usr/local/bin/

# Linux
curl -L https://github.com/ju-net/ecsy/releases/latest/download/ecsy-linux-amd64.gz | gunzip > ecsy
chmod +x ecsy && sudo mv ecsy /usr/local/bin/

# Windows (PowerShell)
Invoke-WebRequest -Uri "https://github.com/ju-net/ecsy/releases/latest/download/ecsy-windows-amd64.exe.gz" -OutFile "ecsy.exe.gz"
gunzip ecsy.exe.gz
```

#### Homebrewでインストール（将来対応予定）

```bash
# brew tap ju-net/tap
# brew install ecsy
```

### ソースからビルド

#### Goがインストールされている場合

```bash
git clone https://github.com/ju-net/ecsy.git
cd ecsy
make deps
make build
make install
```

#### Dockerを使用する場合（Goインストール不要）

```bash
git clone https://github.com/ju-net/ecsy.git
cd ecsy
./docker-build.sh
# dist/ディレクトリにバイナリが生成されます
```

## 使い方

### 基本的な使い方

```bash
# 完全インタラクティブモード（推奨）
ecsy

# プロファイルのみ指定
ecsy -p production

# 一部パラメータを指定（残りは選択）
ecsy -p production -c my-cluster
```

### 完全自動モード

```bash
# すべてのパラメータを指定
ecsy -p production -c my-cluster -s my-service -t task-id

# カスタムコマンドで実行
ecsy -p production -c my-cluster -s my-service -t task-id --command "/bin/bash"
```

### 実行フロー

1. **プロファイル選択**: AWS設定から自動検出、または手動選択
2. **MFA認証** (必要時):
   - MFAデバイスの自動検出・選択
   - MFAコードの入力
   - 一時認証情報の取得
3. **リソース選択**:
   - ECSクラスタ一覧から選択
   - サービス一覧から選択
   - 実行中タスクから選択
4. **コマンド実行**: AWS ECS Execute Commandで接続

### コマンドオプション

| オプション | 短縮形 | 説明 | デフォルト |
|-----------|--------|------|-----------|
| `--profile` | `-p` | AWS プロファイル名 | インタラクティブ選択 |
| `--cluster` | `-c` | ECS クラスタ名 | インタラクティブ選択 |
| `--service` | `-s` | ECS サービス名 | インタラクティブ選択 |
| `--task` | `-t` | ECS タスクID | インタラクティブ選択 |
| `--command` | | 実行するコマンド | `/bin/sh` |
| `--help` | `-h` | ヘルプを表示 | |

## MFA設定

AWS プロファイルでMFAを使用する場合は、`~/.aws/config`に以下のように設定してください：

```ini
[profile production]
region = ap-northeast-1
mfa_serial = arn:aws:iam::123456789012:mfa/username
```

## 必要な権限

実行するIAMユーザー/ロールには以下の権限が必要です：

- `ecs:ListClusters`
- `ecs:ListServices`
- `ecs:ListTasks`
- `ecs:DescribeTasks`
- `ecs:ExecuteCommand`

## ビルド

```bash
# 現在のプラットフォーム向けビルド
make build

# 全プラットフォーム向けビルド
make build-all

# リリース用（圧縮済み）
make release
```

## ライセンス

MIT