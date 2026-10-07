# rust-core —— RustDesk 内核的「独立进程」接入层（方案②）

> 本目录目前**只有方案与骨架**，不包含内核源码、不参与 `desktop/` 的构建。
> 本轮验收不要求真的把内核编出来（需要 vcpkg / clang / llvm 等重依赖）；
> 这里的目标是把「以后怎么接」写成**可执行**的文档与脚本约定。

## 1. 结论先行：为什么是方案②

| 方案 | 做法 | 后果 |
| --- | --- | --- |
| ① 链接内核 | 把 RustDesk 的 Rust 库作为依赖链进 NEILICO 客户端（`flutter_rust_bridge` 直连） | NEILICO 客户端整体被 **AGPL-3.0** 传染；且内核依赖树庞大（vcpkg、FFmpeg、libvpx…），每次构建都要全量编译 |
| **② 进程隔离（采用）** | 内核编译成**独立可执行文件**，由客户端以子进程方式拉起，双方只通过「命令行 + 配置文件 + 自己的网络协议」交互 | 客户端与内核是**两个程序**，进程边界即许可边界（AGPL 的义务落在内核进程这一侧）；内核可独立升级、独立崩溃重启；客户端保持轻量可测 |

**硬约束（必须遵守）**：

1. **不链接**内核代码进 `desktop/`（不把 `rustdesk` 作为 Cargo/CMake 依赖）。
2. **不修改**内核源码（不打 patch，除非另开一个 fork 分支并单独评估许可）。
3. 内核与 `hbbs`/`hbbr` 的通信、画面编解码、加密，全部由内核自己负责；NEILICO **不实现自己的加密协议**。
4. 客户端只拿到并传递**公钥**；私钥只存在于内核自己的身份目录，权限 `0600`。

## 2. 与 hbbs / hbbr 的关系

```
                    ┌──────────────── NEILICO 控制面 (control-plane) ───────────────┐
                    │  nodes / 授权开关 / 设备授权策略 / 下发起服务器参数（只含公钥）  │
                    └───────────────┬──────────────────────────────┬────────────────┘
                                    │ REST (登录用户)               │ 部署/生命周期
                                    ▼                               ▼
                    ┌───────────────────────────┐        ┌──────────────────────────┐
                    │ desktop/ (Flutter 客户端) │        │ hbbs (ID/会合) + hbbr(中继) │
                    │  登录 / 设备网格 / 开关面板 │        │   21115 / 21116 / 21117     │
                    └──────────┬────────────────┘        └────────────▲─────────────┘
                               │ 进程边界（启动/停止/传配置）             │ 内核自己的协议
                               ▼                                        │
                    ┌───────────────────────────┐                         │
                    │ rust-core 内核进程          │─────────────────────────┘
                    │  neilico-kernel            │  取公钥登记 / 建立会话 / 画面链路
                    └───────────────────────────┘
```

- `hbbs`：ID 注册与会合（rendezvous），客户端与内核都向它登记/查询。
- `hbbr`：中继。仅在直连（Mesh / P2P）不可达时使用 —— 与 NEILICO「Mesh 优先」的隧道策略一致。
- 内核与 `hbbs`/`hbbr` 之间是**内核自己的协议**，NEILICO 不参与、不解析。
- 控制面下发的 `id_server` / `relay_server` / `public_key` 三者即内核所需的最小参数集。

## 3. 构建产物路径约定（与客户端代码一一对应）

客户端里的 `lib/services/kernel/kernel_locator.dart` 按下面顺序查找内核，**顺序即约定**：

| 优先级 | 位置 | 用途 |
| --- | --- | --- |
| 1 | 环境变量 `NEILICO_KERNEL_BIN`（绝对路径） | 调试 / 自定义安装 |
| 2 | 与客户端可执行文件同目录：`neilico-kernel`（Windows：`neilico-kernel.exe`） | 打包发行形态 |
| 3 | `<repo>/rust-core/dist/<os>-<arch>/neilico-kernel` | 开发形态 |

`<os>` ∈ {`linux`, `macos`, `windows`}；`<arch>` ∈ {`x86_64`, `aarch64`, `armv7`}。
即 **打包/产出物必须叫 `neilico-kernel`**，并放在上述位置之一。示例：

```
rust-core/dist/linux-x86_64/neilico-kernel
rust-core/dist/macos-aarch64/neilico-kernel
rust-core/dist/windows-x86_64/neilico-kernel.exe
```

> 命名统一的意义：客户端只认一个名字 + 三个位置，**找不到就明确显示「内核未安装」并置灰启动入口**，绝不静默失败。

## 4. 启动 / 停止

命令行契约（客户端 `KernelProcess.buildArguments` 与本约定必须保持一致）：

```
neilico-kernel --config <path/to/kernel.toml> --service
```

- `--config`：客户端生成的运行时配置，见 `config/kernel.example.toml`（只含 `id_server` / `relay_server` / `public_key`）。
- `--service`：以服务/无界面模式运行（不弹内核自己的 GUI，由我们的 Flutter 客户端做界面）。

生命周期：

1. 客户端把配置写到临时目录（`0700`，文件名含随机串），再启动进程。
2. 停止：先 `SIGTERM`，5 秒未退出再 `SIGKILL`（见 `KernelProcess.stop()`）。
3. 崩溃：内核是独立进程，崩溃**不会**拖垮客户端；客户端据此显示「内核已退出」，由用户或守护逻辑决定是否重启。
4. 日志：内核 stdout/stderr 建议重定向到 `~/.local/state/neilico/kernel.log`（轮转交给系统的日志方案）。

## 5. 版本固定方式

内核版本必须**可复现**，否则「客户端能连、别人连不上」这类问题无法定位。

- 上游版本与 commit 固化在 `VERSIONS.yaml`（`upstream_version` + `commit`）。
- 用 **git submodule** 或**固定 tarball + SHA256** 二选一，禁止跟随 `master`。
- 产物命名带版本：`neilico-kernel-<upstream_version>-<os>-<arch>`；`dist/<triple>/neilico-kernel` 是指向当前版本的稳定入口。
- 升级流程：改 `VERSIONS.yaml` → 重建 → 跑 `scripts/smoke-kernel.sh` 冒烟 → 记录到 NOTES。
- 同时固定 `hbbs`/`hbbr` 的镜像 tag，保证「内核 ↔ 服务器」同代。

## 6. flutter_rust_bridge 用在哪里（重要澄清）

很多人会把「接 Rust」默认理解成 `flutter_rust_bridge`。**本方案不用它来接内核**，理由：

- `flutter_rust_bridge` 会把 Rust 代码**静态链接进** Flutter 应用 → 内核的 AGPL 代码进入客户端二进制，方案②的边界就没了。
- 内核依赖树（FFmpeg / libvpx / vcpkg 工具链）会让 `flutter build` 从分钟级变成小时级，且三端难以一致。

**边界选择：**

| 需求 | 选择 |
| --- | --- |
| 内核的画面/会话/中继 | **进程隔离**（本目录）：命令行 + 配置文件 + 内核自己的协议 |
| 我们自己写的、许可宽松的 Rust 辅助逻辑（如本机指纹、性能采集） | 可以上 `flutter_rust_bridge`，但那属于**我们自己的 crate**，与内核无关 |
| 需要读内核状态（会话表、日志） | 优先让内核自己输出（日志/状态文件），其次再考虑 IPC；**不要**为此链接内核 |

结论一句话：**内核永远是「另一个程序」，不是「一个库」。**

## 7. 目录结构

```
rust-core/
├── README.md                  # 本文：接入方案与约定
├── VERSIONS.yaml              # 上游版本 / commit / 服务器镜像 tag 固化
├── config/
│   └── kernel.example.toml    # 客户端生成配置的样例（只含公钥）
├── docs/
│   └── integration-plan.md    # 详细计划：构建、依赖、里程碑、验收
└── scripts/
    ├── fetch-kernel.sh        # 拉取/构建内核的骨架脚本（本轮未执行）
    └── smoke-kernel.sh        # 冒烟：内核能否启动并连上 hbbs
```

## 8. 当前状态

- [x] 客户端侧：内核定位（`KernelLocator`）、启动/停止骨架（`KernelProcess`）、配置生成（`KernelLaunchConfig`）
- [x] 约定：可执行文件名、产物路径、命令行、配置字段
- [ ] 内核实际构建（需要 vcpkg 等重依赖）—— 本轮不做
- [ ] 内核启动/停止的真实联调
- [ ] 与 hbbs/hbbr 的端到端连通性冒烟
