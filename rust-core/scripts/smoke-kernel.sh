#!/usr/bin/env bash
# smoke-kernel.sh —— 内核冒烟：能否按约定被拉起、并完成到 hbbs 的注册。
#
# ⚠️ 本轮【未执行】（内核尚未构建）。语法已通过 `bash -n` 校验。
# 用法：ID_SERVER=rd.neilico.local PUBLIC_KEY=xxx ./smoke-kernel.sh
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
RUST_CORE="$REPO_ROOT/rust-core"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
  darwin) os="macos" ;;
esac
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch="x86_64" ;;
  aarch64|arm64) arch="aarch64" ;;
esac

KERNEL_BIN="${NEILICO_KERNEL_BIN:-$RUST_CORE/dist/$os-$arch/neilico-kernel}"
if [[ ! -x "$KERNEL_BIN" ]]; then
  echo "找不到内核可执行文件：$KERNEL_BIN" >&2
  echo "先跑 scripts/fetch-kernel.sh，或用 NEILICO_KERNEL_BIN 指定。" >&2
  exit 1
fi

: "${ID_SERVER:?需要设置 ID_SERVER（例如 rd.neilico.local）}"
: "${PUBLIC_KEY:?需要设置 PUBLIC_KEY（服务器公钥，不是私钥）}"

TMP_DIR="$(mktemp -d -t neilico-kernel-XXXXXX)"
chmod 700 "$TMP_DIR"
CONFIG="$TMP_DIR/kernel.toml"
LOG="$TMP_DIR/kernel.log"

# 只写公钥；私钥永不经过这里。
cat > "$CONFIG" <<EOF
[server]
id_server = "$ID_SERVER"
relay_server = "${RELAY_SERVER:-$ID_SERVER}"
public_key = "$PUBLIC_KEY"

[render]
kernel_only = true
EOF

echo "==> 启动内核（5 秒内观察是否注册成功）"
"$KERNEL_BIN" --config "$CONFIG" --service > "$LOG" 2>&1 &
KERNEL_PID=$!

cleanup() {
  if kill -0 "$KERNEL_PID" 2>/dev/null; then
    kill -TERM "$KERNEL_PID" 2>/dev/null || true
    sleep 1
    kill -KILL "$KERNEL_PID" 2>/dev/null || true
  fi
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

sleep 5
if ! kill -0 "$KERNEL_PID" 2>/dev/null; then
  echo "内核已退出，日志如下：" >&2
  cat "$LOG" >&2
  exit 2
fi

echo "==> 内核仍在运行，日志尾部："
tail -n 20 "$LOG"

if grep -qiE 'registered|listening|ready' "$LOG"; then
  echo "==> 冒烟通过：内核已完成注册/监听"
  exit 0
fi

echo "==> 内核在跑但没有看到注册关键字，请人工核对日志" >&2
exit 3
