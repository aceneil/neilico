#!/usr/bin/env bash
# fetch-kernel.sh —— 拉取并构建 NEILICO 内核（方案②：编译成独立可执行文件）。
#
# ⚠️ 本轮【未执行】。它是骨架 + 可执行步骤说明：真正跑起来需要
#    vcpkg / clang / llvm / FFmpeg 等重依赖，属于 M1 里程碑的工作。
#    语法已通过 `bash -n` 校验。
#
# 产物：rust-core/dist/<os>-<arch>/neilico-kernel[.exe]
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
RUST_CORE="$REPO_ROOT/rust-core"
WORK_DIR="${NEILICO_KERNEL_BUILD_DIR:-$RUST_CORE/.build}"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
  linux) os="linux" ;;
  darwin) os="macos" ;;
  *) echo "不支持的系统：$os" >&2; exit 1 ;;
esac
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch="x86_64" ;;
  aarch64|arm64) arch="aarch64" ;;
  *) echo "未归档的架构：$arch" >&2; exit 1 ;;
esac
TRIPLE="$os-$arch"
DIST_DIR="$RUST_CORE/dist/$TRIPLE"

echo "==> 目标平台：$TRIPLE"
echo "==> 版本事实来源：$RUST_CORE/VERSIONS.yaml"

# ---------------------------------------------------------------------------
# 步骤 1：读取锁定的上游版本
#   必须来自 VERSIONS.yaml，禁止跟随 master。
# ---------------------------------------------------------------------------
# 需要 yq 或 python3+pyyaml；这里给出两种写法。
ref="$(python3 - "$RUST_CORE/VERSIONS.yaml" <<'PY'
import sys, yaml
doc = yaml.safe_load(open(sys.argv[1]))
print(doc["rustdesk"]["upstream_ref"])
PY
)"
sha="$(python3 - "$RUST_CORE/VERSIONS.yaml" <<'PY'
import sys, yaml
doc = yaml.safe_load(open(sys.argv[1]))
print(doc["rustdesk"]["tarball_sha256"])
PY
)"

if [[ "$ref" == TODO* || "$sha" == TODO* ]]; then
  echo "VERSIONS.yaml 仍是占位值（$ref）。请先在 M1 落地时填入真实 tag/commit/sha256。" >&2
  exit 2
fi

# ---------------------------------------------------------------------------
# 步骤 2：拉取源码并校验哈希（可复现）
# ---------------------------------------------------------------------------
mkdir -p "$WORK_DIR/src"
tarball="$WORK_DIR/rustdesk-$ref.tar.gz"
if [[ ! -f "$tarball" ]]; then
  curl -fL --retry 3 -o "$tarball" \
    "https://github.com/rustdesk/rustdesk/archive/refs/tags/$ref.tar.gz"
fi
echo "$sha  $tarball" | sha256sum -c -

rm -rf "$WORK_DIR/src/rustdesk"
mkdir -p "$WORK_DIR/src/rustdesk"
tar -xzf "$tarball" -C "$WORK_DIR/src/rustdesk" --strip-components=1

# ---------------------------------------------------------------------------
# 步骤 3：构建（需要 vcpkg；具体命令随上游版本变化，必须对照该版本的
#         官方构建文档，不要照抄本脚本而不加验证）
# ---------------------------------------------------------------------------
echo "==> 构建（需要 vcpkg + clang；本步骤在骨架里不自动执行）"
cat <<'HINT'
请在该版本的上游文档指导下执行，典型流程：

  # Linux
  sudo apt-get install -y clang cmake ninja-build pkg-config libgtk-3-dev \
       libxdo-dev libssl-dev libayatana-appindicator3-dev \
       libxcb-randr0-dev libxcb-shape0-dev libxcb-xfixes0-dev libxfixes-dev
  git clone https://github.com/microsoft/vcpkg "$HOME/vcpkg"
  "$HOME/vcpkg/bootstrap-vcpkg.sh"
  export VCPKG_ROOT="$HOME/vcpkg"
  cd .build/src/rustdesk
  cargo build --release --features linux_headless

构建完成后，把产物（cargo 的 target/release/<binary>）复制为：
  rust-core/dist/<os>-<arch>/neilico-kernel
HINT

# ---------------------------------------------------------------------------
# 步骤 4：安装到约定路径 + 写入构建信息
# ---------------------------------------------------------------------------
mkdir -p "$DIST_DIR"
if [[ -x "$WORK_DIR/src/rustdesk/target/release/rustdesk" ]]; then
  cp "$WORK_DIR/src/rustdesk/target/release/rustdesk" "$DIST_DIR/neilico-kernel"
  chmod +x "$DIST_DIR/neilico-kernel"
  {
    echo "upstream_ref: $ref"
    echo "tarball_sha256: $sha"
    echo "built_at: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
    echo "host: $TRIPLE"
    echo "clang: $(clang --version | head -1)"
  } > "$DIST_DIR/BUILD-INFO"
  echo "==> 完成：$DIST_DIR/neilico-kernel"
else
  echo "==> 尚未构建出产物；按上面 HINT 完成构建后重新运行本脚本以完成安装。" >&2
  exit 3
fi
