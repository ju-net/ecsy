# ecsy - ECS Command Execute Utility

[![CI](https://github.com/ju-net/ecsy/workflows/CI/badge.svg)](https://github.com/ju-net/ecsy/actions)
[![Release](https://github.com/ju-net/ecsy/workflows/Release/badge.svg)](https://github.com/ju-net/ecsy/actions)
[![Go Report Card](https://goreportcard.com/badge/github.com/ju-net/ecsy)](https://goreportcard.com/report/github.com/ju-net/ecsy)

MFA認証対応のECS Command Executeを簡単に実行するためのCLIツールです。

## 特徴

- AWS プロファイルの選択（MFA認証対応）
- インタラクティブな選択UI
  - ECSクラスタ一覧から選択
  - サービス一覧から選択  
  - 実行中のタスク一覧から選択
- コマンドライン引数による直接指定も可能
- クロスプラットフォーム対応（macOS, Linux, Windows）

## インストール

### ビルド済みバイナリ

[Releases](https://github.com/ju-net/ecsy/releases)ページから環境に合ったバイナリをダウンロードしてください。

#### 手動インストール

```bash
# macOS (Intel)
curl -L https://github.com/ju-net/ecsy/releases/latest/download/ecsy_${VERSION}_darwin_amd64.tar.gz | tar xz
sudo mv ecsy /usr/local/bin/

# macOS (Apple Silicon)  
curl -L https://github.com/ju-net/ecsy/releases/latest/download/ecsy_${VERSION}_darwin_arm64.tar.gz | tar xz
sudo mv ecsy /usr/local/bin/

# Linux
curl -L https://github.com/ju-net/ecsy/releases/latest/download/ecsy_${VERSION}_linux_amd64.tar.gz | tar xz
sudo mv ecsy /usr/local/bin/

# Windows (PowerShell)
Invoke-WebRequest -Uri "https://github.com/ju-net/ecsy/releases/latest/download/ecsy_${VERSION}_windows_amd64.zip" -OutFile "ecsy.zip"
Expand-Archive ecsy.zip
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

### インタラクティブモード

```bash
# すべてをインタラクティブに選択
ecsy

# プロファイルを指定して実行
ecsy -p production
```

### 直接指定モード

```bash
# すべてのパラメータを指定して実行
ecsy -p production -c my-cluster -s my-service -t arn:aws:ecs:region:account:task/cluster-name/task-id

# コマンドを指定
ecsy -p production -c my-cluster -s my-service -t task-id --command "/bin/bash"
```

### オプション

- `-p, --profile`: AWS プロファイル名
- `-c, --cluster`: ECS クラスタ名
- `-s, --service`: ECS サービス名
- `-t, --task`: ECS タスクID
- `--command`: 実行するコマンド（デフォルト: /bin/sh）

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