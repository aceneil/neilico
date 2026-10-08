# NEILICO 对 rustdesk-server 的 vendoring 说明

本目录是 [`rustdesk/rustdesk-server`](https://github.com/rustdesk/rustdesk-server) 的**源码副本**，
由 NEILICO 的 allinone 镜像**自行编译**出 `hbbs`（ID/信令）与 `hbbr`（中继）两个二进制，
不再依赖外部容器或外部镜像（旧的外部 `rustdesk/rustdesk-server:1` compose 仅作回滚保留）。

## 来源与版本（可复现）

| 项 | 值 |
| :--- | :--- |
| 上游仓库 | https://github.com/rustdesk/rustdesk-server |
| 固定 tag | `1.1.16` |
| 主仓库 commit | `73523b31cfd25d77dee862e6fc9f5e1fb5e485ef` |
| 依赖子模块 | `hbb_common`（https://github.com/rustdesk/hbb_common）commit `83419b6549636ee39dacef7776c473f5802e08d6` |
| 许可 | GNU AGPL-3.0（原文见本目录 `LICENSE`，未改动） |

## 我方相对上游的改动（源码逻辑本身未改）

1. **展平子模块**：上游用 git submodule 引入 `libs/hbb_common`；这里把它**内容化**（普通目录），
   以免构建时依赖外网拉子模块。已删除 `.gitmodules`。
2. **离线依赖**：`vendor.tar.gz` 是 `cargo vendor` 产出的**全部 crate 依赖**（含 git 依赖
   `async-speed-limit` / `reqwest` / `confy` / `tokio-socks` / `sysinfo` / `machine-uid` / `default_net`）。
   构建时先解压为 `vendor/`，配合 `.cargo/config.toml` 的 `vendored-sources` 段即可**完全离线**编译。
3. **保留编译期 schema 库**：上游提交了 `db_v2.sqlite3`（含 `peer` 表）与 `.env`
   （`DATABASE_URL=sqlite://./db_v2.sqlite3`）供 `sqlx::query!` 宏在**编译期**校验 SQL。本目录原样保留二者，
   并在 Dockerfile 里显式 `ENV DATABASE_URL=sqlite://./db_v2.sqlite3`（不依赖 `.env` 也能编译）。
4. `.cargo/config.toml`：在上游原有内容基础上**追加**了 `[source.*] replace-with = "vendored-sources"`
   段落（指向 `vendor/`）。上游原有的 windows/macos rustflags 段保持不变。

除上述构建接线外，**未修改任何上游 Rust 源码文件**。

## 离线构建

```bash
tar -xzf vendor.tar.gz          # 得到 vendor/（约 765MB）
cargo build --release --offline --bin hbbs --bin hbbr
```

allinone 的 `deploy/allinone/Dockerfile` 已在 Rust 构建阶段自动完成上述解压与离线编译，
并把二进制装到最终镜像的 `/usr/local/bin/{hbbs,hbbr}`。

> 安全：`hbbr` 以 `-k _` 启动（从工作目录 `id_ed25519` 取 Key 强制校验），私钥只存在于控制面数据目录，
> 绝不进日志或接口响应。
